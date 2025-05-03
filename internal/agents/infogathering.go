package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/llm"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
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

	// Update status to processing
	log.Printf("Updating status to processing")
	if err := handle.UpdateStatus(protocol.TaskState("processing"), nil); err != nil {
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

	// Update status to analyzing ticket
	log.Printf("Updating status to analyzing_ticket")
	if err := handle.UpdateStatus(protocol.TaskState("analyzing_ticket"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Analyze the ticket information
	log.Printf("Analyzing ticket information")
	analysis := a.analyzeTicketInfo(&task)

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

// extractTaskData extracts task data from the message parts
func (a *InformationGatheringAgent) extractTaskData(message protocol.Message, task *models.TicketAvailableTask) error {
	// Debug: print the message parts
	log.Printf("Message has %d parts", len(message.Parts))

	// Handle case when message has parts
	if len(message.Parts) > 0 {
		// Check each part
		for _, part := range message.Parts {
			// Try to handle TextPart
			if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil && textPart.Text != "" {
				log.Printf("Found TextPart: %s", textPart.Text)
				
				// Try direct unmarshal
				if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
					log.Printf("Successfully parsed task from TextPart")
					// Validate required fields
					if task.TicketID != "" && task.Summary != "" {
						return nil
					} else {
						log.Printf("Parsed task but missing required fields")
					}
				}
			}
		}

		// Try other parsing approaches if direct method fails
		for _, part := range message.Parts {
			partJSON, _ := json.Marshal(part)
			
			// Try to extract ticketId and summary directly from the part
			var directPart struct {
				TicketID string            `json:"ticketId"`
				Summary  string            `json:"summary"`
				Metadata map[string]string `json:"metadata,omitempty"`
			}
			if err := json.Unmarshal(partJSON, &directPart); err == nil {
				if directPart.TicketID != "" && directPart.Summary != "" {
					task.TicketID = directPart.TicketID
					task.Summary = directPart.Summary
					task.Metadata = directPart.Metadata
					log.Printf("Successfully extracted task from part directly: %s - %s", task.TicketID, task.Summary)
					return nil
				}
			}
		}
	}

	// If we got here, we weren't able to extract the task data
	return fmt.Errorf("no valid ticket-available task data found in message")
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
	// Try LLM analysis first if available
	if a.llmClient != nil {
		llmResult, err := a.analyzeWithLLM(task)
		if err == nil {
			log.Printf("Successfully analyzed ticket with LLM")
			return llmResult
		}
		
		log.Printf("LLM analysis failed: %v, falling back to basic analysis", err)
	}
	
	// Fallback to basic analysis if LLM is not available or fails
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

// createLLMPrompt creates a prompt for the LLM based on the ticket information
func (a *InformationGatheringAgent) createLLMPrompt(task *models.TicketAvailableTask) string {
	// Build a structured prompt for the LLM
	prompt := fmt.Sprintf(`You are an expert in analyzing Jira tickets and providing insights. 
Please analyze the following Jira ticket information:

Ticket ID: %s
Summary: %s
`, task.TicketID, task.Summary)

	// Add description if available
	if description, ok := task.Metadata["description"]; ok && description != "" {
		prompt += fmt.Sprintf("Description: %s\n", description)
	}

	// Add other metadata if available
	metadataFields := []string{"priority", "issueType", "reporter", "components", "projectKey"}
	for _, field := range metadataFields {
		if value, ok := task.Metadata[field]; ok && value != "" {
			prompt += fmt.Sprintf("%s: %s\n", capitalize(field), value)
		}
	}

	// Add any changes if present
	changesFound := false
	for key, value := range task.Metadata {
		if strings.HasPrefix(key, "change_") {
			if !changesFound {
				prompt += "\nRecent Changes:\n"
				changesFound = true
			}
			fieldName := strings.TrimPrefix(key, "change_")
			prompt += fmt.Sprintf("- %s: %s\n", capitalize(fieldName), value)
		}
	}

	// Add instructions for the response format
	prompt += `
Please provide a comprehensive analysis in JSON format with the following fields:
{
  "keyThemes": ["theme1", "theme2", ...],
  "riskLevel": "high|medium|low",
  "priority": "high|medium|low",
  "suggestion": "Your main suggestion for handling this ticket",
  "requirements": ["requirement1", "requirement2", ...],
  "technicalAnalysis": "Detailed technical analysis of the issue",
  "businessImpact": "Impact on business operations",
  "nextSteps": "Recommended next steps for handling this ticket",
  "recommendedPriority": "high|medium|low",
  "recommendedComponents": ["component1", "component2", ...],
  "recommendedLabels": ["label1", "label2", ...]
}

Ensure your analysis is concise but comprehensive, covering both technical and business aspects.
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

// performBasicAnalysis is a fallback that analyzes the ticket without using LLM
func (a *InformationGatheringAgent) performBasicAnalysis(task *models.TicketAvailableTask) *AnalysisResult {
	// For basic analysis, we'll do some simple keyword matching
	var result AnalysisResult
	
	// Extract key themes from summary (simulated)
	words := strings.Fields(strings.ToLower(task.Summary))
	themes := make(map[string]bool)
	
	// Look for common themes in the summary
	for _, word := range words {
		switch {
		case strings.Contains(word, "bug") || strings.Contains(word, "fix") || strings.Contains(word, "issue"):
			themes["bug"] = true
		case strings.Contains(word, "feature") || strings.Contains(word, "add") || strings.Contains(word, "new"):
			themes["feature"] = true
		case strings.Contains(word, "improve") || strings.Contains(word, "enhance") || strings.Contains(word, "update"):
			themes["enhancement"] = true
		case strings.Contains(word, "document") || strings.Contains(word, "doc"):
			themes["documentation"] = true
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
	
	// Determine risk level and priority from metadata
	priority := "medium"
	if task.Metadata != nil {
		if p, ok := task.Metadata["priority"]; ok {
			priority = strings.ToLower(p)
		}
	}
	result.Priority = priority
	
	// Set risk level based on priority
	switch priority {
	case "high", "critical", "urgent":
		result.RiskLevel = "high"
	case "medium":
		result.RiskLevel = "medium"
	default:
		result.RiskLevel = "low"
	}
	
	// Generate suggestion based on analysis
	if result.RiskLevel == "high" {
		result.Suggestion = "This is a high-priority task that should be addressed promptly."
	} else if contains(result.KeyThemes, "bug") {
		result.Suggestion = "This bug should be investigated to determine its impact on users."
	} else if contains(result.KeyThemes, "feature") {
		result.Suggestion = "This new feature request should be evaluated for roadmap alignment."
	} else {
		result.Suggestion = "Review this task and assign appropriate resources."
	}
	
	// Add technical analysis
	result.TechnicalAnalysis = "No detailed technical analysis available without LLM."
	
	// Add business impact
	result.BusinessImpact = "Impact on business operations cannot be determined without further analysis."
	
	// Add next steps
	result.NextSteps = "Review this ticket with the team to determine appropriate action."
	
	// Set same priority as recommended
	result.RecommendedPriority = priority
	
	// Simulated requirements extraction
	result.Requirements = []string{
		"Gather more detailed information about the scope",
		"Verify impact on existing functionality",
		"Consider testing requirements",
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
