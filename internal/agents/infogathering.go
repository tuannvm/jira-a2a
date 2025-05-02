package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/pkg/models"
)

// TaskProcessor is the interface that must be implemented by all agents
type TaskProcessor interface {
	Process(ctx context.Context, taskData []byte, handle TaskHandle) error
}

// TaskHandle is the interface for updating task status and recording artifacts
type TaskHandle interface {
	UpdateStatus(status string) error
	RecordArtifact(name, url string) error
	Complete(result []byte) error
}

// InformationGatheringAgent implements the TaskProcessor interface
type InformationGatheringAgent struct {
	config    *config.Config
	jiraClient *jira.Client
}

// NewInformationGatheringAgent creates a new InformationGatheringAgent
func NewInformationGatheringAgent(cfg *config.Config) *InformationGatheringAgent {
	return &InformationGatheringAgent{
		config:    cfg,
		jiraClient: jira.NewClient(cfg),
	}
}

// Process implements the TaskProcessor interface
func (a *InformationGatheringAgent) Process(ctx context.Context, taskData []byte, handle TaskHandle) error {
	// Update status to processing
	if err := handle.UpdateStatus("processing"); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Parse the task data
	var task models.TicketAvailableTask
	if err := json.Unmarshal(taskData, &task); err != nil {
		return fmt.Errorf("failed to unmarshal task data: %w", err)
	}

	// Log the task
	log.Printf("Processing ticket-available task for ticket %s: %s", task.TicketID, task.Summary)

	// Update status to fetching ticket details
	if err := handle.UpdateStatus("fetching ticket details"); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Fetch the ticket details
	ticket, err := a.jiraClient.GetTicket(task.TicketID)
	if err != nil {
		return fmt.Errorf("failed to fetch ticket details: %w", err)
	}

	// Update status to analyzing ticket
	if err := handle.UpdateStatus("analyzing ticket"); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Analyze the ticket for missing fields
	missingFields, collectedFields := a.analyzeTicket(ticket)

	// Update status to posting comment
	if err := handle.UpdateStatus("posting comment"); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// Generate and post a comment
	comment := a.generateComment(ticket, missingFields, collectedFields)
	jiraComment, err := a.jiraClient.PostComment(task.TicketID, comment)
	if err != nil {
		return fmt.Errorf("failed to post comment: %w", err)
	}

	// Record the comment URL as an artifact
	if err := handle.RecordArtifact("comment", jiraComment.URL); err != nil {
		return fmt.Errorf("failed to record artifact: %w", err)
	}

	// Create the info-gathered task
	infoGatheredTask := models.InfoGatheredTask{
		TicketID:        task.TicketID,
		CollectedFields: collectedFields,
		CommentURL:      jiraComment.URL,
	}

	// Marshal the info-gathered task
	result, err := json.Marshal(infoGatheredTask)
	if err != nil {
		return fmt.Errorf("failed to marshal info-gathered task: %w", err)
	}

	// Complete the task
	return handle.Complete(result)
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
