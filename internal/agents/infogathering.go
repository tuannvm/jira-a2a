package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"slices"
)

// InformationGatheringAgent implements the TaskProcessor interface from trpc-a2a-go
// It focuses on analyzing ticket information and generating summaries
type InformationGatheringAgent struct {
	config *config.Config
}

// NewInformationGatheringAgent creates a new InformationGatheringAgent
func NewInformationGatheringAgent(cfg *config.Config) *InformationGatheringAgent {
	return &InformationGatheringAgent{
		config: cfg,
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
	comment := a.generateSummary(&task, analysis)

	// Record the analysis result as an artifact
	log.Printf("Adding artifact with analysis result")
	artifact := protocol.Artifact{
		Name:        stringPtr("analysis"),
		Description: stringPtr("Ticket Analysis"),
		Parts:       []protocol.Part{protocol.NewTextPart(comment)},
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
			"Summary":    task.Summary,
			"Analysis":   "Completed",
			"Suggestion": analysis.Suggestion,
		},
		CommentURL: fmt.Sprintf("https://jira.example.com/browse/%s", task.TicketID), // Placeholder URL, JiraRetrievalAgent will handle actual Jira integration
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
	KeyThemes    []string
	RiskLevel    string
	Priority     string
	Suggestion   string
	Requirements []string
}

// analyzeTicketInfo analyzes the ticket information and produces insights
// This would normally integrate with an LLM for deeper analysis
func (a *InformationGatheringAgent) analyzeTicketInfo(task *models.TicketAvailableTask) *AnalysisResult {
	// In a real implementation, this would use LLM to analyze the ticket
	// For now, we'll do some basic analysis based on the summary and metadata
	
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
	
	// Simulated requirements extraction (in a real implementation, LLM would extract these)
	result.Requirements = []string{
		"Gather more detailed information about the scope",
		"Verify impact on existing functionality",
		"Consider testing requirements",
	}
	
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

	// Add requirements
	sb.WriteString("\n*Requirements:*\n")
	for _, req := range analysis.Requirements {
		sb.WriteString(fmt.Sprintf("- %s\n", req))
	}

	// Add suggestion
	sb.WriteString(fmt.Sprintf("\n*Suggestion:* %s\n", analysis.Suggestion))

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
