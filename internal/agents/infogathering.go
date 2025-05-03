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
	// Create a new Jira client
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

	// Update status to fetching ticket details
	log.Printf("Updating status to fetching_ticket_details")
	if err := handle.UpdateStatus(protocol.TaskState("fetching_ticket_details"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Fetch the ticket details
	log.Printf("Fetching ticket details for %s", task.TicketID)
	ticket, err := a.jiraClient.GetTicket(task.TicketID)
	if err != nil {
		log.Printf("Failed to fetch ticket details: %v", err)
		return fmt.Errorf("failed to fetch ticket details: %w", err)
	}

	// Update status to analyzing ticket
	log.Printf("Updating status to analyzing_ticket")
	if err := handle.UpdateStatus(protocol.TaskState("analyzing_ticket"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Analyze the ticket for missing fields
	log.Printf("Analyzing ticket")
	missingFields, collectedFields := a.analyzeTicket(ticket)

	// Update status to posting comment
	log.Printf("Updating status to posting_comment")
	if err := handle.UpdateStatus(protocol.TaskState("posting_comment"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Generate and post a comment
	log.Printf("Generating and posting comment")
	comment := a.generateComment(ticket, missingFields, collectedFields)
	jiraComment, err := a.jiraClient.PostComment(task.TicketID, comment)
	if err != nil {
		log.Printf("Failed to post comment: %v", err)
		return fmt.Errorf("failed to post comment: %w", err)
	}

	// Record the comment URL as an artifact - use metadata instead of URL
	log.Printf("Adding artifact with comment URL: %s", jiraComment.URL)
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
	log.Printf("Creating info-gathered result")
	infoGatheredTask := models.InfoGatheredTask{
		TicketID:        task.TicketID,
		CollectedFields: collectedFields,
		CommentURL:      jiraComment.URL,
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

		// If we get here, we haven't successfully parsed the task from a TextPart
		// Let's try another approach - check if the specific part has ticketId and summary
		for _, part := range message.Parts {
			partJSON, _ := json.Marshal(part)
			log.Printf("Checking part: %s", string(partJSON))

			// First try to extract type and text fields if this is a TextPart representation
			var typedPart struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(partJSON, &typedPart); err == nil {
				if typedPart.Type == "text" && typedPart.Text != "" {
					log.Printf("Found text type part with text: %s", typedPart.Text)
					
					// Try to unmarshal the text field as our task
					if err := json.Unmarshal([]byte(typedPart.Text), task); err == nil {
						log.Printf("Successfully parsed task from text field")
						if task.TicketID != "" && task.Summary != "" {
							return nil
						}
					}
				}
			}
			
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
	
	// Special handling for cases where we're dealing with a serialized TextPart
	// This is to handle the format seen in the logs: {"type":"text","text":"..."}
	for _, part := range message.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil {
			var partObject struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			
			if err := json.Unmarshal([]byte(textPart.Text), &partObject); err == nil {
				if partObject.Type == "text" && partObject.Text != "" {
					// We may have a nested TextPart representation
					log.Printf("Found nested TextPart: %s", partObject.Text)
					
					// Try to extract the ticket data from this nested text
					if err := json.Unmarshal([]byte(partObject.Text), task); err == nil {
						log.Printf("Successfully parsed task from nested TextPart")
						if task.TicketID != "" && task.Summary != "" {
							return nil
						}
					}
				}
			}
		}
	}

	// If we got here, we weren't able to extract the task data
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
