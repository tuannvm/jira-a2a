package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// InformationGatheringAgent implements the TaskProcessor interface from trpc-a2a-go
type InformationGatheringAgent struct {
	config     *config.Config
	jiraClient *jira.Client
}

// NewInformationGatheringAgent creates a new InformationGatheringAgent
func NewInformationGatheringAgent(cfg *config.Config) *InformationGatheringAgent {
	return &InformationGatheringAgent{
		config:     cfg,
		jiraClient: jira.NewClient(cfg),
	}
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// Process implements the TaskProcessor interface from trpc-a2a-go
func (a *InformationGatheringAgent) Process(ctx context.Context, taskID string, message protocol.Message, handle taskmanager.TaskHandle) error {
	// Update status to processing
	if err := handle.UpdateStatus(protocol.TaskState("processing"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Extract the task data from message
	var task models.TicketAvailableTask
	if err := a.extractTaskData(message, &task); err != nil {
		return fmt.Errorf("failed to extract task data: %w", err)
	}

	// Log the task
	log.Printf("Processing ticket-available task for ticket %s: %s", task.TicketID, task.Summary)

	// Update status to fetching ticket details
	if err := handle.UpdateStatus(protocol.TaskState("fetching_ticket_details"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Fetch the ticket details
	ticket, err := a.jiraClient.GetTicket(task.TicketID)
	if err != nil {
		return fmt.Errorf("failed to fetch ticket details: %w", err)
	}

	// Update status to analyzing ticket
	if err := handle.UpdateStatus(protocol.TaskState("analyzing_ticket"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Analyze the ticket for missing fields
	missingFields, collectedFields := a.analyzeTicket(ticket)

	// Update status to posting comment
	if err := handle.UpdateStatus(protocol.TaskState("posting_comment"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Generate and post a comment
	comment := a.generateComment(ticket, missingFields, collectedFields)
	jiraComment, err := a.jiraClient.PostComment(task.TicketID, comment)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}

	// Record the comment URL as an artifact - use metadata instead of URL
	artifact := protocol.Artifact{
		Name:        stringPtr("comment"),
		Description: stringPtr("Jira Comment"),
		Parts:       []protocol.Part{},
		Metadata: map[string]interface{}{
			"url": jiraComment.URL,
		},
	}
	if err := handle.AddArtifact(artifact); err != nil {
		return fmt.Errorf("failed to record artifact: %w", err)
	}

	// Create the info-gathered result
	infoGatheredTask := models.InfoGatheredTask{
		TicketID:        task.TicketID,
		CollectedFields: collectedFields,
		CommentURL:      jiraComment.URL,
	}

	// Marshal the result to JSON for the response
	resultJSON, err := json.Marshal(infoGatheredTask)
	if err != nil {
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

	return nil
}

// extractTaskData extracts task data from the message parts
func (a *InformationGatheringAgent) extractTaskData(message protocol.Message, task *models.TicketAvailableTask) error {
	// Debug: print the message parts
	log.Printf("Message has %d parts", len(message.Parts))

	// First approach: Check for TextPart
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil && textPart.Text != "" {
			log.Printf("Found TextPart: %s", textPart.Text)

			// Try to unmarshal directly
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				log.Printf("Successfully parsed task from TextPart")
				return nil
			}

			// If direct unmarshaling fails, look for JSON wrapper
			var wrapper map[string]interface{}
			if err := json.Unmarshal([]byte(textPart.Text), &wrapper); err == nil {
				// Try to find a field that could contain our task data
				for _, v := range wrapper {
					if subJson, ok := v.(string); ok {
						if err := json.Unmarshal([]byte(subJson), task); err == nil {
							log.Printf("Successfully parsed task from nested JSON")
							return nil
						}
					} else if subMap, ok := v.(map[string]interface{}); ok {
						// Try to extract ticket ID and summary
						if ticketID, ok := subMap["ticketId"].(string); ok {
							task.TicketID = ticketID

							if summary, ok := subMap["summary"].(string); ok {
								task.Summary = summary

								if metadata, ok := subMap["metadata"].(map[string]interface{}); ok {
									task.Metadata = make(map[string]string)
									for k, v := range metadata {
										if strVal, ok := v.(string); ok {
											task.Metadata[k] = strVal
										}
									}
								}

								log.Printf("Successfully extracted ticket data: %s - %s", task.TicketID, task.Summary)
								return nil
							}
						}
					}
				}
			}
		}
	}

	// Direct approach: if there's only one text part and it contains ticketId and summary
	if len(message.Parts) == 1 {
		partJSON, _ := json.Marshal(message.Parts[0])
		log.Printf("Examining single part: %s", string(partJSON))

		// Try direct extraction
		var wrapper map[string]interface{}
		if err := json.Unmarshal(partJSON, &wrapper); err == nil {
			if ticketID, ok := wrapper["ticketId"].(string); ok {
				task.TicketID = ticketID

				if summary, ok := wrapper["summary"].(string); ok {
					task.Summary = summary

					log.Printf("Successfully extracted ticket data from part: %s - %s", task.TicketID, task.Summary)
					return nil
				}
			}
		}
	}

	// If we get here, no valid task data was found
	return fmt.Errorf("no valid ticket-available task data found in message")
}

// analyzeTicket analyzes a ticket for missing fields and collects available fields
func (a *InformationGatheringAgent) analyzeTicket(ticket *models.JiraTicket) ([]string, map[string]string) {
	missingFields := []string{}
	collectedFields := make(map[string]string)

	// Check for description
	if ticket.Description == "" {
		missingFields = append(missingFields, "Description")
	} else {
		collectedFields["Description"] = "Present"
	}

	// Check for acceptance criteria (assuming it's in the description with a heading)
	if !strings.Contains(strings.ToLower(ticket.Description), "acceptance criteria") {
		missingFields = append(missingFields, "Acceptance Criteria")
	} else {
		collectedFields["Acceptance Criteria"] = "Present"
	}

	// Check for due date
	if ticket.DueDate == "" {
		missingFields = append(missingFields, "Due Date")
	} else {
		collectedFields["Due Date"] = ticket.DueDate
	}

	// Check for links
	if len(ticket.Links) == 0 {
		missingFields = append(missingFields, "Linked Tickets")
	} else {
		collectedFields["Linked Tickets"] = fmt.Sprintf("%d linked tickets", len(ticket.Links))
	}

	return missingFields, collectedFields
}

// generateComment generates a comment for the Jira ticket
func (a *InformationGatheringAgent) generateComment(ticket *models.JiraTicket, missingFields []string, collectedFields map[string]string) string {
	var sb strings.Builder

	sb.WriteString("*Information Gathering Summary*\n\n")
	sb.WriteString("I've analyzed this ticket and gathered the following information:\n\n")

	// Add collected fields
	sb.WriteString("*Collected Information:*\n")
	for field, value := range collectedFields {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", field, value))
	}

	// Add missing fields
	if len(missingFields) > 0 {
		sb.WriteString("\n*Missing Information:*\n")
		for _, field := range missingFields {
			sb.WriteString(fmt.Sprintf("- %s\n", field))
		}
		sb.WriteString("\nPlease update the ticket with the missing information to help the team better understand the requirements.")
	} else {
		sb.WriteString("\nAll required information is present in this ticket. Thank you for providing a complete ticket!")
	}

	return sb.String()
}
