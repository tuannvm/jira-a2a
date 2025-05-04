package agents

import (
	"context"
	"encoding/json"
	"errors"
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
	"slices"
)

// InformationGatheringAgent implements the TaskProcessor interface from trpc-a2a-go
// It focuses on analyzing ticket information and generating summaries without Jira API interactions
type InformationGatheringAgent struct {
	config   *config.Config
	llmClient llm.LLMClient
	// No Jira client as this agent doesn't interact with Jira API
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
			log.Printf("Falling back to basic analysis without LLM")
		}
	} else {
		log.Printf("LLM is disabled in config, using basic analysis")
	}
	
	return &InformationGatheringAgent{
		config:   cfg,
		llmClient: llmClient,
	}
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// Process implements the TaskProcessor interface from trpc-a2a-go
func (a *InformationGatheringAgent) Process(ctx context.Context, taskID string, message protocol.Message, handle taskmanager.TaskHandle) error {
	// Log the incoming message for debugging
	log.Printf("Received task with ID: %s", taskID)
	messageJSON, _ := json.Marshal(message)
	log.Printf("Raw message: %s", string(messageJSON))

	// Update status to processing with a message for better SSE support
	log.Printf("Updating status to processing")
	processingMsg := &protocol.Message{
		Parts: []protocol.Part{protocol.NewTextPart("Processing task...")},
	}
	if err := handle.UpdateStatus(protocol.TaskState("processing"), processingMsg); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Extract the task data from message
	var task models.TicketAvailableTask
	log.Printf("Extracting task data")
	if err := a.extractTaskData(message, &task); err != nil {
		log.Printf("Failed to extract task data: %v", err)
		return fmt.Errorf("failed to extract task data: %w", err)
	}

	// Log the task
	log.Printf("Processing ticket-available task for ticket %s: %s", task.TicketID, task.Summary)

	// The task should already contain all necessary ticket details provided by JiraRetrievalAgent
	// No need to fetch ticket details from Jira API

	// Update status to analyzing ticket with a message for better SSE support
	log.Printf("Updating status to analyzing_ticket")
	analyzingMsg := &protocol.Message{
		Parts: []protocol.Part{protocol.NewTextPart(fmt.Sprintf("Analyzing ticket %s: %s...", task.TicketID, task.Summary))},
	}
	if err := handle.UpdateStatus(protocol.TaskState("analyzing_ticket"), analyzingMsg); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Analyze the ticket information
	log.Printf("Analyzing ticket information")
	analysis := a.analyzeTicketInfo(&task)

	// Update status to generating summary with a message for better SSE support
	log.Printf("Updating status to generating_summary")
	generatingMsg := &protocol.Message{
		Parts: []protocol.Part{protocol.NewTextPart("Generating summary...")},
	}
	if err := handle.UpdateStatus(protocol.TaskState("generating_summary"), generatingMsg); err != nil {
		log.Printf("Warning: failed to update status to generating_summary: %v", err)
		// Continue despite the error
	}

	// Generate a summary
	log.Printf("Generating summary")
	summary := a.generateSummary(&task, analysis)

	// Record the analysis result as an artifact
	log.Printf("Adding artifact with analysis result")
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
	log.Printf("Creating info-gathered result")
	infoGatheredTask := models.InfoGatheredTask{
		TicketID: task.TicketID,
		CollectedFields: map[string]string{
			"Summary":          task.Summary,
			"Analysis":         "Completed",
			"Suggestion":       analysis.Suggestion,
			"RiskLevel":        analysis.RiskLevel,
			"Priority":         analysis.Priority,
			"KeyThemes":        strings.Join(analysis.KeyThemes, ", "),
			"Requirements":     strings.Join(analysis.Requirements, ", "),
			"LLMGenerated":     fmt.Sprintf("%v", analysis.LLMUsed),
			"TechnicalAnalysis": analysis.TechnicalAnalysis,
			"BusinessImpact":   analysis.BusinessImpact,
			"NextSteps":        analysis.NextSteps,
		},
		// No CommentURL as this agent doesn't interact with Jira API
	}
	
	// Add recommended fields if available
	if analysis.RecommendedPriority != "" {
		infoGatheredTask.CollectedFields["RecommendedPriority"] = analysis.RecommendedPriority
	}
	
	if len(analysis.RecommendedComponents) > 0 {
		infoGatheredTask.CollectedFields["RecommendedComponents"] = strings.Join(analysis.RecommendedComponents, ", ")
	}
	
	if len(analysis.RecommendedLabels) > 0 {
		infoGatheredTask.CollectedFields["RecommendedLabels"] = strings.Join(analysis.RecommendedLabels, ", ")
	}

	// Marshal the result to JSON for the response
	resultJSON, err := json.Marshal(infoGatheredTask)
	if err != nil {
		log.Printf("Failed to marshal info-gathered task: %v", err)
		return fmt.Errorf("failed to marshal info-gathered task: %w", err)
	}

	// Create the response message with the info-gathered result
	log.Printf("Creating response message")
	textPart := protocol.NewTextPart(string(resultJSON))
	responseMsg := &protocol.Message{
		Parts: []protocol.Part{textPart},
	}

	// Complete the task with the response
	log.Printf("Completing task")
	if err := handle.UpdateStatus(protocol.TaskState("completed"), responseMsg); err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	log.Printf("Task %s completed successfully", taskID)
	return nil
}

// extractTaskData extracts generic task data from the message parts
func (a *InformationGatheringAgent) extractTaskData(message protocol.Message, task *models.TicketAvailableTask) error {
	// Debug: print the message parts
	log.Printf("Message has %d parts", len(message.Parts))

	// Handle case when message has no parts
	if len(message.Parts) == 0 {
		return fmt.Errorf("message has no parts")
	}

	// Print the raw message for debugging
	msgBytes, _ := json.Marshal(message)
	log.Printf("Raw message: %s", string(msgBytes))

	// Try each part one by one
	for _, part := range message.Parts {
		// Check if it's a DataPart
		dataPart, ok := part.(*protocol.DataPart)
		if ok && dataPart != nil {
			log.Printf("Processing DataPart")

			// Handle the data as a generic map
			if data, ok := dataPart.Data.(map[string]interface{}); ok {
				log.Printf("DataPart contains a map")
				
				// Initialize metadata map
				task.Metadata = make(map[string]string)
				
				// Extract all fields generically without assuming specific field names
				for key, value := range data {
					switch key {
					case "id", "taskId", "ticketId": // Handle ID field generically
						if strVal, ok := value.(string); ok {
							task.TicketID = strVal
						} else {
							task.TicketID = fmt.Sprintf("%v", value)
						}
					case "title", "summary", "description": // Handle summary field generically
						if strVal, ok := value.(string); ok {
							task.Summary = strVal
						} else {
							task.Summary = fmt.Sprintf("%v", value)
						}
					case "metadata": // Handle metadata as a nested map
						if metaMap, ok := value.(map[string]interface{}); ok {
							for metaKey, metaValue := range metaMap {
								if strVal, ok := metaValue.(string); ok {
									task.Metadata[metaKey] = strVal
								} else {
									task.Metadata[metaKey] = fmt.Sprintf("%v", metaValue)
								}
							}
						}
					default: // All other fields go to metadata
						if strVal, ok := value.(string); ok {
							task.Metadata[key] = strVal
						} else if mapVal, ok := value.(map[string]interface{}); ok {
							// Handle nested maps by flattening them
							for nestedKey, nestedValue := range mapVal {
								if nestedStrVal, ok := nestedValue.(string); ok {
									task.Metadata[key+"_"+nestedKey] = nestedStrVal
								} else {
									task.Metadata[key+"_"+nestedKey] = fmt.Sprintf("%v", nestedValue)
								}
							}
						} else {
							task.Metadata[key] = fmt.Sprintf("%v", value)
						}
					}
				}
				
				// Ensure we have at least some values
				if task.TicketID == "" {
					task.TicketID = "UNKNOWN-ID"
				}
				if task.Summary == "" {
					task.Summary = "No summary available"
				}

				log.Printf("Extracted generic task data: ID=%s, Summary=%s, MetadataFields=%d", task.TicketID, task.Summary, len(task.Metadata))
				return nil
			} else {
				// If data isn't a map, try to marshal and unmarshal it
				dataBytes, err := json.Marshal(dataPart.Data)
				if err == nil {
					if err := json.Unmarshal(dataBytes, task); err == nil {
						// Ensure we have at least some values
						if task.TicketID == "" {
							task.TicketID = "UNKNOWN-ID"
						}
						if task.Summary == "" {
							task.Summary = "No summary available"
						}
						if task.Metadata == nil {
							task.Metadata = make(map[string]string)
						}
						log.Printf("Converted DataPart to generic task: %s - %s", task.TicketID, task.Summary)
						return nil
					}
				}
				
				return fmt.Errorf("failed to parse data part")
			}
		}
	}

	return fmt.Errorf("failed to extract task data from message parts")
}

// AnalysisResult represents the analysis of a ticket
type AnalysisResult struct {
	KeyThemes        []string
	RiskLevel        string
	Priority         string
	Suggestion       string
	Requirements     []string
	LLMUsed          bool
	Confidence       float64
	TechnicalAnalysis string
	BusinessImpact   string
	NextSteps        string
	RecommendedPriority string
	RecommendedComponents []string
	RecommendedLabels []string
}

// analyzeTicketInfo analyzes the ticket information and produces insights
// This would normally integrate with an LLM for deeper analysis
func (a *InformationGatheringAgent) analyzeTicketInfo(task *models.TicketAvailableTask) *AnalysisResult {
	// Use LLM analysis if available, otherwise use basic analysis
	if a.llmClient != nil {
		llmResult, err := a.analyzeWithLLM(task)
		if err == nil {
			log.Printf("Successfully analyzed ticket with LLM")
			return llmResult
		}
		
		// If LLM analysis fails, return the error instead of falling back
		return &AnalysisResult{
			KeyThemes:        []string{"error"},
			RiskLevel:        "unknown",
			Priority:         "unknown",
			Suggestion:       "LLM analysis failed",
			Requirements:     []string{"Fix LLM integration"},
			LLMUsed:          false,
			Confidence:       0.0,
			TechnicalAnalysis: fmt.Sprintf("LLM analysis error: %v", err),
			BusinessImpact:   "Unable to determine due to LLM error",
			NextSteps:        "Check LLM configuration and try again",
		}
	}
	
	// Use basic analysis if LLM is not available
	return a.performBasicAnalysis(task)
}

// analyzeWithLLM performs analysis using the LLM
func (a *InformationGatheringAgent) analyzeWithLLM(task *models.TicketAvailableTask) (*AnalysisResult, error) {
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
	
	// Mark as LLM-generated
	result.LLMUsed = true
	
	return result, nil
}

// createLLMPrompt creates a prompt for the LLM based on the task information
func (a *InformationGatheringAgent) createLLMPrompt(task *models.TicketAvailableTask) string {
	// Build a structured prompt for the LLM
	// Note: This is where we inform the LLM about Jira, not in the code logic
	prompt := fmt.Sprintf(`You are an expert in analyzing information and providing insights. 
You're currently working with a Jira ticket system. Please analyze the following information:

Item ID: %s
Summary: %s
`, task.TicketID, task.Summary)

	// Add description if available - look for it under various possible keys
	description := ""
	for key, value := range task.Metadata {
		if strings.Contains(strings.ToLower(key), "description") || 
		   strings.Contains(strings.ToLower(key), "content") || 
		   strings.Contains(strings.ToLower(key), "details") {
			description = value
			break
		}
	}
	if description != "" {
		prompt += fmt.Sprintf("Description: %s\n", description)
	}

	// Add all metadata in a generic way
	prompt += "\nAdditional Information:\n"
	for key, value := range task.Metadata {
		// Skip metadata that might be too large or not useful for analysis
		if strings.Contains(strings.ToLower(key), "description") || 
		   strings.HasPrefix(key, "raw_") || 
		   len(value) > 500 {
			continue
		}
		
		// Handle changes specially
		if strings.HasPrefix(key, "change_") {
			fieldName := strings.TrimPrefix(key, "change_")
			prompt += fmt.Sprintf("Changed %s: %s\n", capitalize(fieldName), value)
		} else {
			prompt += fmt.Sprintf("%s: %s\n", capitalize(key), value)
		}
	}

	// Add instructions for the response format
	// This includes Jira-specific terminology in the prompt, not in the code
	prompt += `
Please provide a comprehensive analysis in JSON format with the following fields:
{
  "keyThemes": ["theme1", "theme2", ...],
  "riskLevel": "high|medium|low",
  "priority": "high|medium|low",
  "suggestion": "Your main suggestion for handling this Jira ticket",
  "requirements": ["requirement1", "requirement2", ...],
  "technicalAnalysis": "Detailed technical analysis of the issue",
  "businessImpact": "Impact on business operations",
  "nextSteps": "Recommended next steps for handling this Jira ticket",
  "recommendedPriority": "high|medium|low",
  "recommendedComponents": ["component1", "component2", ...],
  "recommendedLabels": ["label1", "label2", ...]
}

Ensure your analysis is concise but comprehensive, covering both technical and business aspects.
Remember that this is a Jira ticket, so use appropriate terminology and consider best practices for Jira ticket management.
`

	return prompt
}

// parseLLMResponse parses the LLM response and extracts structured information
func (a *InformationGatheringAgent) parseLLMResponse(response string) (*AnalysisResult, error) {
	// Try to extract JSON from the response
	jsonStr, err := extractJSON(response)
	if err != nil {
		return nil, fmt.Errorf("failed to extract JSON from response: %w", err)
	}
	
	// Parse the JSON into a map
	var jsonResponse map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &jsonResponse); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	
	// Create a new AnalysisResult
	result := &AnalysisResult{
		LLMUsed:    true,
		Confidence: 0.9, // Default confidence for LLM responses
	}
	
	// Extract key themes
	if themes, ok := jsonResponse["keyThemes"].([]interface{}); ok {
		for _, theme := range themes {
			if themeStr, ok := theme.(string); ok {
				result.KeyThemes = append(result.KeyThemes, themeStr)
			}
		}
	}
	
	// Extract risk level
	if riskLevel, ok := jsonResponse["riskLevel"].(string); ok {
		result.RiskLevel = normalizeRiskLevel(riskLevel)
	}
	
	// Extract priority
	if priority, ok := jsonResponse["priority"].(string); ok {
		result.Priority = normalizePriority(priority)
	}
	
	// Extract suggestion
	if suggestion, ok := jsonResponse["suggestion"].(string); ok {
		result.Suggestion = suggestion
	}
	
	// Extract requirements
	if requirements, ok := jsonResponse["requirements"].([]interface{}); ok {
		for _, req := range requirements {
			if reqStr, ok := req.(string); ok {
				result.Requirements = append(result.Requirements, reqStr)
			}
		}
	}
	
	// Extract technical analysis
	if technicalAnalysis, ok := jsonResponse["technicalAnalysis"].(string); ok {
		result.TechnicalAnalysis = technicalAnalysis
	}
	
	// Extract business impact
	if businessImpact, ok := jsonResponse["businessImpact"].(string); ok {
		result.BusinessImpact = businessImpact
	}
	
	// Extract next steps
	if nextSteps, ok := jsonResponse["nextSteps"].(string); ok {
		result.NextSteps = nextSteps
	}
	
	// Extract recommended priority
	if recommendedPriority, ok := jsonResponse["recommendedPriority"].(string); ok {
		result.RecommendedPriority = normalizePriority(recommendedPriority)
	}
	
	// Extract recommended components
	if components, ok := jsonResponse["recommendedComponents"].([]interface{}); ok {
		for _, comp := range components {
			if compStr, ok := comp.(string); ok {
				result.RecommendedComponents = append(result.RecommendedComponents, compStr)
			}
		}
	}
	
	// Extract recommended labels
	if labels, ok := jsonResponse["recommendedLabels"].([]interface{}); ok {
		for _, label := range labels {
			if labelStr, ok := label.(string); ok {
				result.RecommendedLabels = append(result.RecommendedLabels, labelStr)
			}
		}
	}
	
	return result, nil
}

// extractJSON attempts to extract JSON from a string
func extractJSON(text string) (string, error) {
	// Find the first opening brace
	start := strings.Index(text, "{")
	if start == -1 {
		return "", errors.New("no JSON found in response")
	}
	
	// Find the last closing brace
	end := strings.LastIndex(text, "}")
	if end == -1 {
		return "", errors.New("no closing brace found in response")
	}
	
	// Extract the potential JSON string
	jsonStr := text[start : end+1]
	
	// Validate that it's valid JSON
	var js json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &js); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	
	return jsonStr, nil
}

// normalizeRiskLevel normalizes risk level strings to standard values
func normalizeRiskLevel(level string) string {
	level = strings.ToLower(level)
	
	switch level {
	case "high", "critical", "severe", "important":
		return "high"
	case "medium", "moderate", "normal":
		return "medium"
	case "low", "minor", "trivial":
		return "low"
	default:
		return "medium" // Default to medium if unknown
	}
}

// normalizePriority normalizes priority strings to standard values
func normalizePriority(priority string) string {
	priority = strings.ToLower(priority)
	
	switch priority {
	case "high", "critical", "urgent", "important":
		return "high"
	case "medium", "normal", "moderate":
		return "medium"
	case "low", "minor", "trivial":
		return "low"
	default:
		return "medium" // Default to medium if unknown
	}
}

// performBasicAnalysis provides a generic analysis of task data without using LLM
func (a *InformationGatheringAgent) performBasicAnalysis(task *models.TicketAvailableTask) *AnalysisResult {
	// For basic analysis, we'll do some simple keyword matching
	var result AnalysisResult
	
	// Extract key themes from summary using generic keywords
	words := strings.Fields(strings.ToLower(task.Summary))
	themes := make(map[string]bool)
	
	// Look for common themes in the summary - using generic categories
	for _, word := range words {
		switch {
		case strings.Contains(word, "bug") || strings.Contains(word, "fix") || strings.Contains(word, "issue") || strings.Contains(word, "error") || strings.Contains(word, "problem"):
			themes["problem"] = true
		case strings.Contains(word, "feature") || strings.Contains(word, "add") || strings.Contains(word, "new") || strings.Contains(word, "create"):
			themes["new_functionality"] = true
		case strings.Contains(word, "improve") || strings.Contains(word, "enhance") || strings.Contains(word, "update") || strings.Contains(word, "upgrade"):
			themes["improvement"] = true
		case strings.Contains(word, "document") || strings.Contains(word, "doc") || strings.Contains(word, "guide") || strings.Contains(word, "manual"):
			themes["documentation"] = true
		case strings.Contains(word, "research") || strings.Contains(word, "investigate") || strings.Contains(word, "analyze") || strings.Contains(word, "explore"):
			themes["research"] = true
		}
	}
	
	// Convert themes to slice
	for theme := range themes {
		result.KeyThemes = append(result.KeyThemes, theme)
	}
	
	// If no themes detected, add "task" as default
	if len(result.KeyThemes) == 0 {
		result.KeyThemes = append(result.KeyThemes, "task")
	}
	
	// Determine priority from metadata generically
	priority := "medium" // Default priority
	if task.Metadata != nil {
		// Look for priority in metadata with various possible keys
		for key, value := range task.Metadata {
			if strings.Contains(strings.ToLower(key), "priority") || strings.Contains(strings.ToLower(key), "importance") || strings.Contains(strings.ToLower(key), "urgency") {
				priority = strings.ToLower(value)
				break
			}
		}
	}
	result.Priority = priority
	
	// Set risk level based on priority generically
	switch {
	case strings.Contains(priority, "high") || strings.Contains(priority, "critical") || strings.Contains(priority, "urgent") || strings.Contains(priority, "important"):
		result.RiskLevel = "high"
	case strings.Contains(priority, "medium") || strings.Contains(priority, "normal") || strings.Contains(priority, "moderate"):
		result.RiskLevel = "medium"
	default:
		result.RiskLevel = "low"
	}
	
	// Generate suggestion based on analysis generically
	if result.RiskLevel == "high" {
		result.Suggestion = "This is a high-priority item that should be addressed promptly."
	} else if contains(result.KeyThemes, "problem") {
		result.Suggestion = "This issue should be investigated to determine its impact."
	} else if contains(result.KeyThemes, "new_functionality") {
		result.Suggestion = "This new functionality should be evaluated for alignment with goals."
	} else if contains(result.KeyThemes, "improvement") {
		result.Suggestion = "This improvement could enhance existing functionality."
	} else {
		result.Suggestion = "Review this item and assign appropriate resources."
	}
	
	// Add generic technical analysis
	result.TechnicalAnalysis = "No detailed technical analysis available without LLM."
	
	// Add generic business impact
	result.BusinessImpact = "Impact on operations cannot be determined without further analysis."
	
	// Add generic next steps
	result.NextSteps = "Review this item with the team to determine appropriate action."
	
	// Set same priority as recommended
	result.RecommendedPriority = priority
	
	// Generic requirements
	result.Requirements = []string{
		"Gather more detailed information about the scope",
		"Assess impact on existing systems",
		"Consider testing and validation requirements",
	}
	
	// Mark as not LLM-generated
	result.LLMUsed = false
	result.Confidence = 0.5 // Lower confidence for basic analysis
	
	return &result
}

// generateSummary creates a formatted summary based on the analysis
func (a *InformationGatheringAgent) generateSummary(task *models.TicketAvailableTask, analysis *AnalysisResult) string {
	var sb strings.Builder

	sb.WriteString("*Information Gathering Summary*\n\n")
	sb.WriteString(fmt.Sprintf("I've analyzed ticket %s: \"%s\" and gathered the following information:\n\n", 
		task.TicketID, task.Summary))

	// Add key themes
	sb.WriteString("*Key Themes:*\n")
	for _, theme := range analysis.KeyThemes {
		sb.WriteString(fmt.Sprintf("- %s\n", capitalize(theme)))
	}

	// Add risk assessment
	sb.WriteString(fmt.Sprintf("\n*Risk Assessment:* %s\n", capitalize(analysis.RiskLevel)))
	sb.WriteString(fmt.Sprintf("*Priority:* %s\n", capitalize(analysis.Priority)))
	
	// Add technical analysis if available
	if analysis.TechnicalAnalysis != "" {
		sb.WriteString(fmt.Sprintf("\n*Technical Analysis:*\n%s\n", analysis.TechnicalAnalysis))
	}
	
	// Add business impact if available
	if analysis.BusinessImpact != "" {
		sb.WriteString(fmt.Sprintf("\n*Business Impact:*\n%s\n", analysis.BusinessImpact))
	}

	// Add requirements
	sb.WriteString("\n*Requirements:*\n")
	for _, req := range analysis.Requirements {
		sb.WriteString(fmt.Sprintf("- %s\n", req))
	}
	
	// Add recommended changes
	if analysis.RecommendedPriority != "" && analysis.RecommendedPriority != analysis.Priority {
		sb.WriteString(fmt.Sprintf("\n*Recommended Priority:* %s\n", capitalize(analysis.RecommendedPriority)))
	}
	
	if len(analysis.RecommendedComponents) > 0 {
		sb.WriteString("\n*Recommended Components:*\n")
		for _, comp := range analysis.RecommendedComponents {
			sb.WriteString(fmt.Sprintf("- %s\n", comp))
		}
	}
	
	if len(analysis.RecommendedLabels) > 0 {
		sb.WriteString("\n*Recommended Labels:*\n")
		for _, label := range analysis.RecommendedLabels {
			sb.WriteString(fmt.Sprintf("- %s\n", label))
		}
	}
	
	// Add next steps if available
	if analysis.NextSteps != "" {
		sb.WriteString(fmt.Sprintf("\n*Next Steps:*\n%s\n", analysis.NextSteps))
	}

	// Add suggestion
	sb.WriteString(fmt.Sprintf("\n*Suggestion:* %s\n", analysis.Suggestion))
	
	// Add footer with info about analysis method
	if analysis.LLMUsed {
		sb.WriteString("\n_This analysis was generated with AI assistance._")
	} else {
		sb.WriteString("\n_This analysis was generated using basic heuristics. Enable LLM for more detailed analysis._")
	}

	return sb.String()
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

// Helper function to capitalize the first letter of a string
func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// SetupServer creates and configures the A2A server for the InformationGatheringAgent
func (a *InformationGatheringAgent) SetupServer() (*server.A2AServer, error) {
	// Define the agent card
	agentCard := server.AgentCard{
		Name:        a.config.AgentName,
		Description: stringPtr("Agent that gathers information from tickets"),
		URL:         a.config.AgentURL,
		Version:     a.config.AgentVersion,
		Provider: &server.AgentProvider{
			Organization: "Your Organization",
			URL:          stringPtr("https://example.com"),
		},
		Capabilities: server.AgentCapabilities{
			Streaming:              true,
			StateTransitionHistory: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []server.AgentSkill{
			{
				ID:          "process-ticket-available",
				Name:        "Process Ticket Available",
				Description: stringPtr("Processes a new or updated ticket, gathering information and generating analysis"),
				Tags:        []string{"ticket", "information-gathering"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
		},
	}

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(a)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	// Setup server options
	serverOpts := []server.Option{}

	// Add authentication if enabled
	if a.config.AuthType != "" {
		var authProvider auth.Provider
		switch a.config.AuthType {
		case "jwt":
			authProvider = auth.NewJWTAuthProvider(
				[]byte(a.config.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			apiKeys := map[string]string{
				a.config.APIKey: "user",
			}
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			return nil, fmt.Errorf("unsupported auth type: %s", a.config.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
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

	// Wait for interrupt signal
	<-ctx.Done()

	// Create a context with a timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server
	log.Println("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}
