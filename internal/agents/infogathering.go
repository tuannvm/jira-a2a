package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/llm"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/log" // Import trpc-a2a-go logging package with alias
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
			log.Warnf("Warning: Failed to initialize LLM client: %v", err)
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
			log.Infof("Configuring JWT authentication for InformationGatheringAgent")
			authProvider = auth.NewJWTAuthProvider(
				[]byte(a.config.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			log.Infof("Configuring API key authentication for InformationGatheringAgent (API key length: %d)", len(a.config.APIKey))
			// The key is the expected API key value, and the value is the user identifier (not used in this case)
			// The header name must match what the client is using (X-API-Key)
			apiKeys := map[string]string{
				a.config.APIKey: "user",
			}
			log.Infof("API key authentication configured with header: X-API-Key")
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			log.Warnf("Warning: Unsupported authentication type '%s'", a.config.AuthType)
			return nil, fmt.Errorf("unsupported auth type: %s", a.config.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
	} else {
		log.Warnf("Warning: No authentication configured for InformationGatheringAgent")
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
		log.Infof("Starting A2A server on %s", addr)
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
	log.Infof("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

// Process implements the TaskProcessor interface
// It receives a task from JiraRetrievalAgent, extracts data, analyzes it using LLM, and returns the results
func (a *InformationGatheringAgent) Process(ctx context.Context, taskID string, message protocol.Message, handle taskmanager.TaskHandle) error {
	// Log the incoming message for debugging
	log.Info("Received task with ID: %s", taskID)

	// Log detailed information about the message
	log.Debug("Message contains %d parts", len(message.Parts))
	for i, part := range message.Parts {
		log.Debug("Message part %d type: %T", i, part)

		// Log DataPart details
		if dataPart, ok := part.(*protocol.DataPart); ok && dataPart != nil {
			log.Debug("DataPart %d data type: %T", i, dataPart.Data)

			// Log detailed information about the DataPart.Data
			switch data := dataPart.Data.(type) {
			case []byte:
				previewLen := 200
				if len(data) < previewLen {
					previewLen = len(data)
				}
				log.Debug("DataPart %d is []byte (%d bytes): %s", i, len(data), string(data[:previewLen]))
				// Try to unmarshal as JSON to see the structure
				var jsonData map[string]interface{}
				if err := json.Unmarshal(data, &jsonData); err == nil {
					keys := make([]string, 0, len(jsonData))
					for k := range jsonData {
						keys = append(keys, k)
					}
					log.Debug("DataPart %d JSON keys: %v", i, keys)
				} else {
					log.Debug("DataPart %d is not valid JSON: %v", i, err)
				}
			case string:
				previewLen := 200
				if len(data) < previewLen {
					previewLen = len(data)
				}
				log.Debug("DataPart %d is string (%d chars): %s", i, len(data), data[:previewLen])
				// Try to unmarshal as JSON to see the structure
				var jsonData map[string]interface{}
				if err := json.Unmarshal([]byte(data), &jsonData); err == nil {
					keys := make([]string, 0, len(jsonData))
					for k := range jsonData {
						keys = append(keys, k)
					}
					log.Debug("DataPart %d JSON keys: %v", i, keys)
				} else {
					log.Debug("DataPart %d is not valid JSON: %v", i, err)
				}
			case nil:
				log.Debug("DataPart %d is nil", i)
			default:
				log.Debug("DataPart %d is %T: %v", i, data, data)
			}
		}

		// Log TextPart details
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil {
			previewLen := 100
			if len(textPart.Text) < previewLen {
				previewLen = len(textPart.Text)
			}
			log.Debug("TextPart %d preview (%d chars): %s", i, len(textPart.Text), textPart.Text[:previewLen])
		}
	}

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
		log.Infof("Failed to marshal info-gathered task: %v", err)
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

	log.Infof("Task %s completed successfully", taskID)
	return nil
}

// extractTaskData extracts task data from the message parts
// It handles different message formats and nested JSON structures from Jira webhooks
func (a *InformationGatheringAgent) extractTaskData(message protocol.Message, task *models.TicketAvailableTask) error {
	log.Debug("Message has %d parts", len(message.Parts))

	// Handle case when message has no parts
	if len(message.Parts) == 0 {
		log.Error("Message has no parts")
		return fmt.Errorf("message has no parts")
	}

	// Special handling for webhook payload - try to extract directly from the raw JSON
	for _, part := range message.Parts {
		// Try to get the raw data as JSON string
		if dataPart, ok := part.(*protocol.DataPart); ok && dataPart != nil {
			// Try to get the raw data
			var rawData []byte
			var rawJSON string

			// Handle different data types
			switch data := dataPart.Data.(type) {
			case []byte:
				rawData = data
				rawJSON = string(data)
				log.Debug("DataPart contains []byte data, length: %d", len(data))
			case string:
				rawData = []byte(data)
				rawJSON = data
				log.Debug("DataPart contains string data, length: %d", len(data))
			default:
				log.Debug("DataPart contains unsupported data type: %T", data)
				continue
			}

			// Log the raw data for debugging
			previewLen := 200
			if len(rawJSON) < previewLen {
				previewLen = len(rawJSON)
			}
			log.Debug("Raw data preview: %s", rawJSON[:previewLen])

			// Try to directly extract webhook payload fields
			var webhookPayload struct {
				TicketID    string `json:"ticketId"`
				Summary     string `json:"summary"`
				Description string `json:"description"`
				Event       string `json:"event"`
			}

			if err := json.Unmarshal(rawData, &webhookPayload); err == nil {
				log.Info("Successfully parsed webhook payload: ticketId=%s, summary=%s, event=%s",
					webhookPayload.TicketID, webhookPayload.Summary, webhookPayload.Event)

				// If we have the required fields, use them
				if webhookPayload.TicketID != "" && webhookPayload.Summary != "" {
					task.TicketID = webhookPayload.TicketID
					task.Summary = webhookPayload.Summary
					task.Description = webhookPayload.Description

					// Add event to metadata
					if task.Metadata == nil {
						task.Metadata = make(map[string]string)
					}
					task.Metadata["event"] = webhookPayload.Event

					log.Info("Successfully extracted webhook payload data")
					return nil
				}
			} else {
				log.Error("Failed to parse webhook payload: %v", err)
			}
		}
	}

	// Try each part one by one
	for i, part := range message.Parts {
		log.Debug("Processing message part %d of type: %T", i, part)

		// Try DataPart first
		if dataPart, ok := part.(*protocol.DataPart); ok && dataPart != nil {
			log.Debug("Found DataPart")

			// Get data as bytes
			data, ok := dataPart.Data.([]byte)
			if !ok {
				log.Debug("DataPart.Data is not []byte: %T", dataPart.Data)
				// Try to convert to string and then to bytes if possible
				if strData, strOk := dataPart.Data.(string); strOk {
					log.Debug("DataPart.Data is string, converting to bytes, length: %d", len(strData))
					data = []byte(strData)
					// Log first 100 chars of data for debugging
					previewLen := 100
					if len(strData) < previewLen {
						previewLen = len(strData)
					}
					log.Debug("Data preview: %s", strData[:previewLen])
				} else {
					log.Debug("Unable to convert DataPart.Data to usable format")
					continue
				}
			} else {
				// Log first 100 bytes of data for debugging
				previewLen := 100
				if len(data) < previewLen {
					previewLen = len(data)
				}
				log.Debug("DataPart contains %d bytes of data, preview: %s", len(data), string(data[:previewLen]))
			}

			// Try to unmarshal the data directly
			if err := json.Unmarshal(data, task); err == nil {
				log.Debug("Direct JSON unmarshal to task succeeded")
				// Validate required fields
				if task.TicketID != "" && task.Summary != "" {
					log.Info("Successfully extracted task data from DataPart: TicketID=%s, Summary=%s",
						task.TicketID, task.Summary)
					return nil
				} else {
					log.Debug("Direct unmarshal succeeded but required fields missing: TicketID='%s', Summary='%s'",
						task.TicketID, task.Summary)
				}
			} else {
				log.Debug("Direct JSON unmarshal failed: %v", err)
			}

			// If direct unmarshal failed, try to parse as map
			var dataMap map[string]interface{}
			if err := json.Unmarshal(data, &dataMap); err == nil {
				log.Debug("Parsed DataPart as map with %d keys", len(dataMap))
				// Log the keys for debugging
				keys := make([]string, 0, len(dataMap))
				for k := range dataMap {
					keys = append(keys, k)
				}
				log.Debug("Map keys: %v", keys)

				// Extract data from map
				if err := a.extractFromMap(dataMap, task); err == nil {
					log.Info("Successfully extracted task data from map: TicketID=%s, Summary=%s",
						task.TicketID, task.Summary)
					return nil
				} else {
					log.Debug("Failed to extract from map: %v", err)
				}
			} else {
				log.Debug("Failed to parse DataPart as map: %v", err)
			}
		}

		// Try TextPart as fallback
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil {
			log.Debug("Found TextPart with length: %d", len(textPart.Text))
			// Log first 100 chars of text for debugging
			previewLen := 100
			if len(textPart.Text) < previewLen {
				previewLen = len(textPart.Text)
			}
			log.Debug("TextPart preview: %s", textPart.Text[:previewLen])

			// Try to unmarshal as JSON
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				log.Debug("Direct JSON unmarshal from TextPart succeeded")
				// Validate required fields
				if task.TicketID != "" && task.Summary != "" {
					log.Info("Successfully extracted task data from TextPart JSON: TicketID=%s, Summary=%s",
						task.TicketID, task.Summary)
					return nil
				} else {
					log.Debug("TextPart unmarshal succeeded but required fields missing: TicketID='%s', Summary='%s'",
						task.TicketID, task.Summary)
				}
			} else {
				log.Debug("Direct JSON unmarshal from TextPart failed: %v", err)
			}

			// If direct unmarshal failed, try to parse as map
			var dataMap map[string]interface{}
			if err := json.Unmarshal([]byte(textPart.Text), &dataMap); err == nil {
				log.Debug("Parsed TextPart as map with %d keys", len(dataMap))
				// Log the keys for debugging
				keys := make([]string, 0, len(dataMap))
				for k := range dataMap {
					keys = append(keys, k)
				}
				log.Debug("TextPart map keys: %v", keys)

				// Extract data from map
				if err := a.extractFromMap(dataMap, task); err == nil {
					log.Info("Successfully extracted task data from TextPart map: TicketID=%s, Summary=%s",
						task.TicketID, task.Summary)
					return nil
				} else {
					log.Debug("Failed to extract from TextPart map: %v", err)
				}
			} else {
				log.Debug("Failed to parse TextPart as map: %v", err)
			}
		}
	}

	log.Error("All extraction methods failed, could not extract task data")
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
	log.Debug("Map contains keys: %s", keysStr)

	// Log the actual content of each key for debugging
	log.Debug("--- Begin Map Content Debug ---")
	for k, v := range data {
		switch val := v.(type) {
		case string:
			log.Debug("Key '%s' is string: '%s'", k, val)
		case float64, int, int64, float32:
			log.Debug("Key '%s' is number: %v", k, val)
		case bool:
			log.Debug("Key '%s' is boolean: %v", k, val)
		case map[string]interface{}:
			subKeys := make([]string, 0, len(val))
			for sk := range val {
				subKeys = append(subKeys, sk)
			}
			log.Debug("Key '%s' is map with keys: %v", k, subKeys)
		case []interface{}:
			log.Debug("Key '%s' is array with %d elements", k, len(val))
		case nil:
			log.Debug("Key '%s' is nil", k)
		default:
			log.Debug("Key '%s' is %T", k, val)
		}
	}
	log.Debug("--- End Map Content Debug ---")

	// Extract ticket ID
	log.Debug("Attempting to extract ticketId")
	if ticketID, ok := getStringValue(data, "ticketId", "ticket_id", "id"); ok {
		log.Debug("Found ticketId: %s", ticketID)
		task.TicketID = ticketID
	} else {
		log.Debug("No ticketId found at top level")
	}

	// Extract summary
	log.Debug("Attempting to extract summary")
	if summary, ok := getStringValue(data, "summary", "title", "name"); ok {
		log.Debug("Found summary: %s", summary)
		task.Summary = summary
	} else {
		log.Debug("No summary found at top level")
	}

	// Extract description
	log.Debug("Attempting to extract description")
	if description, ok := getStringValue(data, "description", "desc", "content"); ok {
		log.Debug("Found description (length: %d)", len(description))
		task.Description = description
	} else {
		log.Debug("No description found at top level")
	}

	// Extract issue
	log.Debug("Checking for 'issue' key")
	if issueVal, issueExists := data["issue"]; issueExists {
		log.Debug("Found 'issue' key of type: %T", issueVal)

		if issue, ok := issueVal.(map[string]interface{}); ok {
			log.Debug("'issue' is a map with %d keys", len(issue))

			// Log issue keys
			issueKeys := make([]string, 0, len(issue))
			for k := range issue {
				issueKeys = append(issueKeys, k)
			}
			log.Debug("Issue keys: %v", issueKeys)

			// If ticketID not found at top level, try in issue
			if task.TicketID == "" {
				log.Debug("Attempting to extract ticketId from issue")
				if id, ok := getStringValue(issue, "id", "key"); ok {
					log.Debug("Found ticketId in issue: %s", id)
					task.TicketID = id
				} else {
					log.Debug("No ticketId found in issue")
				}
			}

			// Extract fields
			log.Debug("Checking for 'fields' key in issue")
			if fieldsVal, fieldsExist := issue["fields"]; fieldsExist {
				log.Debug("Found 'fields' key of type: %T", fieldsVal)

				if fields, ok := fieldsVal.(map[string]interface{}); ok {
					log.Debug("'fields' is a map with %d keys", len(fields))

					// Log fields keys
					fieldsKeys := make([]string, 0, len(fields))
					for k := range fields {
						fieldsKeys = append(fieldsKeys, k)
					}
					log.Debug("Fields keys: %v", fieldsKeys)

					// If summary not found at top level, try in fields
					if task.Summary == "" {
						log.Debug("Attempting to extract summary from fields")
						if summary, ok := getStringValue(fields, "summary"); ok {
							log.Debug("Found summary in fields: %s", summary)
							task.Summary = summary
						} else {
							log.Debug("No summary found in fields")
						}
					}

					// If description not found at top level, try in fields
					if task.Description == "" {
						log.Debug("Attempting to extract description from fields")
						if description, ok := getStringValue(fields, "description"); ok {
							log.Debug("Found description in fields (length: %d)", len(description))
							task.Description = description
						} else {
							log.Debug("No description found in fields")
						}
					}
				} else {
					log.Debug("'fields' is not a map: %T", fieldsVal)
				}
			} else {
				log.Debug("No 'fields' key found in issue")
			}
		} else {
			log.Debug("'issue' is not a map: %T", issueVal)
		}
	} else {
		log.Debug("No 'issue' key found")
	}

	// Extract metadata
	log.Debug("Setting up metadata map")
	if task.Metadata == nil {
		task.Metadata = make(map[string]string)
	}

	// Extract metadata from top level
	log.Debug("Extracting metadata from top level")
	metadataKeys := []string{"priority", "status", "issueType", "reporter", "assignee", "created", "updated"}
	log.Debug("Looking for metadata keys: %v", metadataKeys)
	for _, key := range metadataKeys {
		if value, ok := getStringValue(data, key); ok {
			log.Debug("Found metadata '%s': %s", key, value)
			task.Metadata[key] = value
		}
	}

	// Extract metadata from nested structures
	log.Debug("Checking for 'metadata' key")
	if metadataVal, metadataExists := data["metadata"]; metadataExists {
		log.Debug("Found 'metadata' key of type: %T", metadataVal)

		if metadata, ok := metadataVal.(map[string]interface{}); ok {
			log.Debug("'metadata' is a map with %d keys", len(metadata))

			// Log metadata keys
			metadataKeys := make([]string, 0, len(metadata))
			for k := range metadata {
				metadataKeys = append(metadataKeys, k)
			}
			log.Debug("Metadata keys: %v", metadataKeys)

			for k, v := range metadata {
				if strValue, ok := v.(string); ok {
					log.Debug("Adding metadata '%s': %s", k, strValue)
					task.Metadata[k] = strValue
				} else {
					log.Debug("Metadata value for '%s' is not a string: %T", k, v)
				}
			}
		} else {
			log.Debug("'metadata' is not a map: %T", metadataVal)
		}
	} else {
		log.Debug("No 'metadata' key found")
	}

	// Validate required fields
	log.Debug("Validating required fields: TicketID='%s', Summary='%s'", task.TicketID, task.Summary)
	if task.TicketID == "" || task.Summary == "" {
		log.Error("Required fields missing after extraction")
		return fmt.Errorf("required fields missing after extraction")
	}

	log.Info("Successfully extracted all required data")
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
