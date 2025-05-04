package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/llm"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// InformationGatheringAgent is a pure information summarizer that analyzes Jira ticket information
// It receives ticket data from JiraRetrievalAgent, analyzes it using LLM, and returns structured insights
// It does not interact directly with the Jira API
type InformationGatheringAgent struct {
	config    *config.Config
	llmClient llm.LLMClient
	server    *server.A2AServer
}

// NewInformationGatheringAgent creates a new InformationGatheringAgent
func NewInformationGatheringAgent(cfg *config.Config) *InformationGatheringAgent {
	var llmClient llm.LLMClient
	
	// Initialize LLM client if enabled
	if cfg.LLMEnabled {
		var err error
		llmClient, err = llm.NewClient(cfg)
		if err != nil {
			log.Printf("Warning: Failed to initialize LLM client: %v", err)
		}
	}
	
	// Note: No Jira client initialization as this agent doesn't interact with Jira API
	return &InformationGatheringAgent{
		config:    cfg,
		llmClient: llmClient,
	}
}

// SetupServer creates and configures the A2A server for the InformationGatheringAgent
func (a *InformationGatheringAgent) SetupServer() (*server.A2AServer, error) {
	// Define the agent card
	agentCard := server.AgentCard{
		Name:        a.config.AgentName,
		Description: stringPtr("Agent that analyzes Jira ticket information"),
		URL:         a.config.AgentURL,
		Version:     a.config.AgentVersion,
		Provider: &server.AgentProvider{
			Organization: "Your Organization",
		},
		DefaultInputModes:  []string{"text", "data"},
		DefaultOutputModes: []string{"text", "data"},
		Skills: []server.AgentSkill{
			{
				ID:          "process-ticket-info",
				Name:        "Process Ticket Information",
				Description: stringPtr("Analyzes ticket information and provides insights"),
				Tags:        []string{"analysis", "ticket"},
				InputModes:  []string{"text", "data"},
				OutputModes: []string{"text", "data"},
			},
		},
	}

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(a)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	// Create server options
	serverOpts := []server.Option{}

	// Add authentication if configured
	if a.config.AuthType != "" {
		var authProvider auth.Provider
		switch a.config.AuthType {
		case "jwt":
			log.Printf("Configuring JWT authentication for InformationGatheringAgent")
			authProvider = auth.NewJWTAuthProvider(
				[]byte(a.config.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			log.Printf("Configuring API key authentication for InformationGatheringAgent (API key length: %d)", len(a.config.APIKey))
			// The key is the expected API key value, and the value is the user identifier (not used in this case)
			// The header name must match what the client is using (X-API-Key)
			apiKeys := map[string]string{
				a.config.APIKey: "user",
			}
			log.Printf("API key authentication configured with header: X-API-Key")
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			log.Printf("Warning: Unsupported authentication type '%s'", a.config.AuthType)
			return nil, fmt.Errorf("unsupported auth type: %s", a.config.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
	} else {
		log.Printf("Warning: No authentication configured for InformationGatheringAgent")
	}

	// Create the server
	srv, err := server.NewA2AServer(agentCard, taskManager, serverOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	return srv, nil
}

// StartServer starts the A2A server and handles graceful shutdown
func (a *InformationGatheringAgent) StartServer(ctx context.Context) error {
	// Setup the server
	srv, err := a.SetupServer()
	if err != nil {
		return fmt.Errorf("failed to setup server: %w", err)
	}

	// Start the server in a goroutine
	addr := fmt.Sprintf("%s:%d", a.config.ServerHost, a.config.ServerPort)
	go func() {
		log.Printf("Starting A2A server on %s", addr)
		if err := srv.Start(addr); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Store the server reference
	a.server = srv

	// Wait for interrupt signal
	<-ctx.Done()

	// Create a context with a timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server
	log.Printf("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

// Process implements the TaskProcessor interface
// It receives a task from JiraRetrievalAgent, extracts data, analyzes it using LLM, and returns the results
func (a *InformationGatheringAgent) Process(ctx context.Context, taskID string, message protocol.Message, handle taskmanager.TaskHandle) error {
	// Log the incoming message for debugging
	log.Printf("Received task with ID: %s", taskID)
	
	// Update status to processing
	if err := handle.UpdateStatus(protocol.TaskState("processing"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	
	// Extract the task data from message
	// This should include all necessary ticket details provided by JiraRetrievalAgent
	var task models.TicketAvailableTask
	if err := a.extractTaskData(message, &task); err != nil {
		return fmt.Errorf("failed to extract task data: %w", err)
	}
	
	// Update status to analyzing ticket
	if err := handle.UpdateStatus(protocol.TaskState("analyzing_ticket"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	
	// Analyze the ticket information using LLM
	analysis, err := a.analyzeTicketInfo(&task)
	if err != nil {
		return fmt.Errorf("failed to analyze ticket: %w", err)
	}
	
	// Generate a summary using LLM
	summary, err := a.generateSummary(&task, analysis)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}
	
	// Record the analysis result as an artifact
	artifact := protocol.Artifact{
		Name:        stringPtr("analysis"),
		Description: stringPtr("Ticket Analysis"),
		Parts:       []protocol.Part{protocol.NewTextPart(summary)},
		Metadata: map[string]interface{}{
			"ticketId": task.TicketID,
		},
	}
	if err := handle.AddArtifact(artifact); err != nil {
		return fmt.Errorf("failed to record artifact: %w", err)
	}
	
	// Create the info-gathered result
	infoGatheredTask := models.InfoGatheredTask{
		TicketID:        task.TicketID,
		CollectedFields: analysis,
	}
	
	// Marshal the result to JSON for the response
	resultJSON, err := json.Marshal(infoGatheredTask)
	if err != nil {
		log.Printf("Failed to marshal info-gathered task: %v", err)
		return fmt.Errorf("failed to marshal info-gathered task: %w", err)
	}

	// Create the response message with the info-gathered result
	textPart := protocol.NewTextPart(string(resultJSON))
	responseMsg := &protocol.Message{
		Parts: []protocol.Part{textPart},
	}

	// Complete the task with the response
	if err := handle.UpdateStatus(protocol.TaskState("completed"), responseMsg); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	log.Printf("Task %s completed successfully", taskID)
	return nil
}

// extractTaskData extracts task data from the message parts
// It handles different message formats and nested JSON structures from Jira webhooks
func (a *InformationGatheringAgent) extractTaskData(message protocol.Message, task *models.TicketAvailableTask) error {
	log.Printf("Message has %d parts", len(message.Parts))
	
	// Handle case when message has no parts
	if len(message.Parts) == 0 {
		return fmt.Errorf("message has no parts")
	}
	
	// Try each part one by one
	for _, part := range message.Parts {
		log.Printf("Processing message part of type: %T", part)
		
		// Try DataPart first
		if dataPart, ok := part.(*protocol.DataPart); ok && dataPart != nil {
			log.Printf("Found DataPart")
			
			// Get data as bytes
			data, ok := dataPart.Data.([]byte)
			if !ok {
				log.Printf("DataPart.Data is not []byte: %T", dataPart.Data)
				continue
			}
			
			// Try to unmarshal the data directly
			if err := json.Unmarshal(data, task); err == nil {
				// Validate required fields
				if task.TicketID != "" && task.Summary != "" {
					log.Printf("Successfully extracted task data from DataPart")
					return nil
				}
			}
			
			// If direct unmarshal failed, try to parse as map
			var dataMap map[string]interface{}
			if err := json.Unmarshal(data, &dataMap); err == nil {
				log.Printf("Parsed DataPart as map with %d keys", len(dataMap))
				
				// Extract data from map
				if err := a.extractFromMap(dataMap, task); err == nil {
					log.Printf("Successfully extracted task data from map")
					return nil
				}
			}
		}
		
		// Try TextPart as fallback
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil {
			log.Printf("Found TextPart with length: %d", len(textPart.Text))
			
			// Try to unmarshal as JSON
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				// Validate required fields
				if task.TicketID != "" && task.Summary != "" {
					log.Printf("Successfully extracted task data from TextPart JSON")
					return nil
				}
			}
			
			// If direct unmarshal failed, try to parse as map
			var dataMap map[string]interface{}
			if err := json.Unmarshal([]byte(textPart.Text), &dataMap); err == nil {
				log.Printf("Parsed TextPart as map with %d keys", len(dataMap))
				
				// Extract data from map
				if err := a.extractFromMap(dataMap, task); err == nil {
					log.Printf("Successfully extracted task data from map")
					return nil
				}
			}
		}
	}
	
	return fmt.Errorf("failed to extract task data from message parts")
}

// extractFromMap extracts task data from a map representation of the JSON
// It handles nested structures and different data types
func (a *InformationGatheringAgent) extractFromMap(data map[string]interface{}, task *models.TicketAvailableTask) error {
	// Log the keys for debugging
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	// Join keys with comma separator
	keysStr := ""
	for i, k := range keys {
		if i > 0 {
			keysStr += ", "
		}
		keysStr += k
	}
	log.Printf("Map contains keys: %s", keysStr)
	
	// Extract ticket ID
	if ticketID, ok := getStringValue(data, "ticketId", "ticket_id", "id"); ok {
		task.TicketID = ticketID
	}
	
	// Extract summary
	if summary, ok := getStringValue(data, "summary", "title", "name"); ok {
		task.Summary = summary
	}
	
	// Extract description
	if description, ok := getStringValue(data, "description", "desc", "content"); ok {
		task.Description = description
	}
	
	// Extract issue
	if issue, ok := data["issue"].(map[string]interface{}); ok {
		// If ticketID not found at top level, try in issue
		if task.TicketID == "" {
			if id, ok := getStringValue(issue, "id", "key"); ok {
				task.TicketID = id
			}
		}
		
		// Extract fields
		if fields, ok := issue["fields"].(map[string]interface{}); ok {
			// If summary not found at top level, try in fields
			if task.Summary == "" {
				if summary, ok := getStringValue(fields, "summary"); ok {
					task.Summary = summary
				}
			}
			
			// If description not found at top level, try in fields
			if task.Description == "" {
				if description, ok := getStringValue(fields, "description"); ok {
					task.Description = description
				}
			}
		}
	}
	
	// Extract metadata
	if task.Metadata == nil {
		task.Metadata = make(map[string]string)
	}
	
	// Extract metadata from top level
	for _, key := range []string{"priority", "status", "issueType", "reporter", "assignee", "created", "updated"} {
		if value, ok := getStringValue(data, key); ok {
			task.Metadata[key] = value
		}
	}
	
	// Extract metadata from nested structures
	if metadata, ok := data["metadata"].(map[string]interface{}); ok {
		for k, v := range metadata {
			if strValue, ok := v.(string); ok {
				task.Metadata[k] = strValue
			}
		}
	}
	
	// Validate required fields
	if task.TicketID == "" || task.Summary == "" {
		return fmt.Errorf("required fields missing after extraction")
	}
	
	return nil
}

// analyzeTicketInfo analyzes the ticket information using LLM
// It always uses LLM for analysis when available
func (a *InformationGatheringAgent) analyzeTicketInfo(task *models.TicketAvailableTask) (map[string]string, error) {
	// Check if LLM client is available
	if a.llmClient == nil {
		return nil, fmt.Errorf("LLM client not available")
	}
	
	// Create a prompt for the LLM
	prompt := a.createLLMPrompt(task)
	
	// Call the LLM for completion
	response, err := a.llmClient.Complete(context.Background(), prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}
	
	// Parse the LLM response
	result, err := a.parseLLMResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}
	
	// Add LLM usage indicator
	result["LLMGenerated"] = "true"
	
	return result, nil
}

// createLLMPrompt creates a prompt for the LLM based on the ticket information
func (a *InformationGatheringAgent) createLLMPrompt(task *models.TicketAvailableTask) string {
	// Create a base prompt with instructions
	prompt := fmt.Sprintf(`You are an expert in analyzing Jira tickets and providing insights. 
Please analyze the following Jira ticket information:

Ticket ID: %s
Summary: %s
`, task.TicketID, task.Summary)
	
	// Add description if available
	if task.Description != "" {
		prompt += fmt.Sprintf("Description: %s\n", task.Description)
	}
	
	// Add metadata
	for k, v := range task.Metadata {
		prompt += fmt.Sprintf("%s: %s\n", capitalize(k), v)
	}
	
	// Add instructions for JSON response format
	prompt += `
Please provide a comprehensive analysis in JSON format. Include the following fields as appropriate for this ticket:

- KeyThemes: List of key themes or topics identified in the ticket
- RiskLevel: Assessment of risk (high, medium, low)
- Priority: Suggested priority level
- TechnicalAnalysis: Technical assessment of the issue
- BusinessImpact: Impact on business operations
- NextSteps: Recommended next steps
- RecommendedPriority: Suggested priority if different from current
- RecommendedComponents: Suggested components that should be associated
- RecommendedLabels: Suggested labels that should be added

You may include additional fields that you think are relevant to this specific ticket.
Ensure your analysis is concise but comprehensive, covering both technical and business aspects.
`

	return prompt
}

// parseLLMResponse parses the LLM response into a structured result
func (a *InformationGatheringAgent) parseLLMResponse(response string) (map[string]string, error) {
	// Extract JSON from the response
	jsonStr, err := extractJSON(response)
	if err != nil {
		return nil, fmt.Errorf("failed to extract JSON from response: %w", err)
	}
	
	// Parse the JSON into a map
	var resultMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &resultMap); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	
	// Convert to string map
	result := make(map[string]string)
	for k, v := range resultMap {
		switch value := v.(type) {
		case string:
			result[k] = value
		case []interface{}:
			// Convert array to comma-separated string
			strArr := make([]string, 0, len(value))
			for _, item := range value {
				if str, ok := item.(string); ok {
					strArr = append(strArr, str)
				}
			}
			// Join the strings with comma separator
			joined := ""
			for i, str := range strArr {
				if i > 0 {
					joined += ", "
				}
				joined += str
			}
			result[k] = joined
		default:
			// Convert other types to string
			result[k] = fmt.Sprintf("%v", value)
		}
	}
	
	return result, nil
}

// generateSummary generates a summary of the analysis using LLM
func (a *InformationGatheringAgent) generateSummary(task *models.TicketAvailableTask, analysis map[string]string) (string, error) {
	// Check if LLM client is available
	if a.llmClient == nil {
		return "", fmt.Errorf("LLM client not available")
	}
	
	// Create a prompt for summary generation
	prompt := fmt.Sprintf(`Based on the following Jira ticket and analysis, create a comprehensive summary:

Ticket ID: %s
Summary: %s
`, task.TicketID, task.Summary)
	
	// Add description if available
	if task.Description != "" {
		prompt += fmt.Sprintf("Description: %s\n", task.Description)
	}
	
	// Add analysis results
	prompt += "\nAnalysis Results:\n"
	for k, v := range analysis {
		prompt += fmt.Sprintf("%s: %s\n", capitalize(k), v)
	}
	
	// Add instructions for summary format
	prompt += `
Please create a well-formatted summary that includes:

1. A brief overview of the ticket
2. Key findings from the analysis
3. Recommendations and next steps

Format the summary in a clear, readable way with appropriate sections and bullet points where needed.
`

	// Call the LLM for completion
	response, err := a.llmClient.Complete(context.Background(), prompt)
	if err != nil {
		return "", fmt.Errorf("LLM summary generation failed: %w", err)
	}
	
	return response, nil
}

// Helper functions

// extractJSON extracts JSON from a text string
func extractJSON(text string) (string, error) {
	// Find the first opening brace
	start := strings.Index(text, "{")
	if start == -1 {
		return "", fmt.Errorf("no JSON found in text")
	}
	
	// Find the last closing brace
	end := strings.LastIndex(text, "}")
	if end == -1 || end <= start {
		return "", fmt.Errorf("incomplete JSON in text")
	}
	
	// Extract the JSON string
	jsonStr := text[start : end+1]
	
	// Validate the JSON
	var tmp interface{}
	if err := json.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	
	return jsonStr, nil
}

// getStringValue gets a string value from a map using multiple possible keys
func getStringValue(data map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			if strValue, ok := value.(string); ok {
				return strValue, true
			}
		}
	}
	return "", false
}

// capitalize capitalizes the first letter of a string
func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Note: stringPtr function is already defined at the top of the file
