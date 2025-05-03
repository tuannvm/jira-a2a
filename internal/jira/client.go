package jira

import (
	"context"
	"fmt"
	"log"

	"github.com/ctreminiom/go-atlassian/v2/jira/v2"
	"github.com/ctreminiom/go-atlassian/v2/pkg/infra/models"
	"github.com/tuannvm/jira-a2a/internal/config"
	internalModels "github.com/tuannvm/jira-a2a/internal/models"
)

// Client represents a Jira API client
type Client struct {
	Config     *config.Config
	JiraClient *v2.Client
	Ctx        context.Context
}

// NewClient creates a new Jira client
func NewClient(cfg *config.Config) *Client {
	// Create a background context
	ctx := context.Background()

	// Initialize the Jira client
	jiraClient, err := v2.New(nil, cfg.JiraBaseURL)
	if err != nil {
		log.Printf("Warning: Error initializing Jira client: %v", err)
		// We'll return a client anyway, but operations will likely fail
	}

	// Set authentication
	if jiraClient != nil {
		jiraClient.Auth.SetBasicAuth(cfg.JiraUsername, cfg.JiraAPIToken)
	}

	return &Client{
		Config:     cfg,
		JiraClient: jiraClient,
		Ctx:        ctx,
	}
}

// GetTicket fetches a Jira ticket by its ID
func (c *Client) GetTicket(ticketID string) (*internalModels.JiraTicket, error) {
	if c.JiraClient == nil {
		return nil, fmt.Errorf("jira client not initialized")
	}
	
	// Define fields to retrieve and expand options
	fields := []string{"summary", "description", "duedate", "issuelinks", "status", "priority", "resolution", 
		"assignee", "reporter", "issuetype", "project", "created", "updated", "components", "labels"}
	expand := []string{} // No expansion needed for now
	
	// Fetch the issue with relevant fields
	issue, response, err := c.JiraClient.Issue.Get(c.Ctx, ticketID, fields, expand)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get issue, status: %d", response.StatusCode)
	}

	// Create our JiraTicket model
	ticket := &internalModels.JiraTicket{
		ID:          issue.ID,
		Key:         issue.Key,
		Summary:     issue.Fields.Summary,
		Description: issue.Fields.Description,
		Fields:      make(map[string]interface{}),
		Links:       []internalModels.JiraLink{},
	}
	
	// Handle due date (safely converted to string)
	if issue.Fields.DueDate != nil {
		ticket.DueDate = fmt.Sprintf("%v", issue.Fields.DueDate)
	}

	// Extract basic fields into the Fields map
	if issue.Fields.Status != nil {
		ticket.Fields["status"] = issue.Fields.Status.Name
	}
	
	if issue.Fields.Priority != nil {
		ticket.Fields["priority"] = issue.Fields.Priority.Name
	}
	
	if issue.Fields.Resolution != nil {
		ticket.Fields["resolution"] = issue.Fields.Resolution.Name
	}
	
	if issue.Fields.Assignee != nil {
		ticket.Fields["assignee"] = issue.Fields.Assignee.DisplayName
	}
	
	if issue.Fields.Reporter != nil {
		ticket.Fields["reporter"] = issue.Fields.Reporter.DisplayName
	}
	
	if issue.Fields.IssueType != nil {
		ticket.Fields["issueType"] = issue.Fields.IssueType.Name
	}
	
	if issue.Fields.Project != nil {
		ticket.Fields["project"] = issue.Fields.Project.Name
	}
	
	// Handle datetime fields
	if issue.Fields.Created != nil {
		ticket.Fields["created"] = fmt.Sprintf("%v", issue.Fields.Created)
	}
	
	if issue.Fields.Updated != nil {
		ticket.Fields["updated"] = fmt.Sprintf("%v", issue.Fields.Updated)
	}
	
	// Handle array fields
	if issue.Fields.Components != nil {
		components := []string{}
		for _, component := range issue.Fields.Components {
			if component != nil {
				components = append(components, component.Name)
			}
		}
		if len(components) > 0 {
			ticket.Fields["components"] = components
		}
	}
	
	if issue.Fields.Labels != nil && len(issue.Fields.Labels) > 0 {
		ticket.Fields["labels"] = issue.Fields.Labels
	}

	// Extract issue links if available
	if issue.Fields.IssueLinks != nil && len(issue.Fields.IssueLinks) > 0 {
		for _, link := range issue.Fields.IssueLinks {
			jiraLink := internalModels.JiraLink{
				Type: link.Type.Name,
			}

			if link.InwardIssue != nil {
				jiraLink.InwardIssue = link.InwardIssue.Key
			}

			if link.OutwardIssue != nil {
				jiraLink.OutwardIssue = link.OutwardIssue.Key
			}

			ticket.Links = append(ticket.Links, jiraLink)
		}
	}

	return ticket, nil
}

// PostComment posts a comment to a Jira ticket
func (c *Client) PostComment(ticketID, commentText string) (*internalModels.JiraComment, error) {
	if c.JiraClient == nil {
		return nil, fmt.Errorf("jira client not initialized")
	}

	// Create the comment payload for v2
	commentPayload := &models.CommentPayloadSchemeV2{
		Body: commentText,
	}

	// Post the comment to the issue using the v2 method
	responseComment, response, err := c.JiraClient.Issue.Comment.Add(c.Ctx, ticketID, commentPayload, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to post comment: %w", err)
	}

	if response.StatusCode != 201 {
		return nil, fmt.Errorf("failed to post comment, status: %d", response.StatusCode)
	}

	// Create our JiraComment model
	jiraComment := &internalModels.JiraComment{
		ID:      responseComment.ID,
		Body:    responseComment.Body,
		Created: responseComment.Created,
	}

	// Extract author if available
	if responseComment.Author != nil {
		jiraComment.Author = responseComment.Author.DisplayName
	}

	// Add the URL
	jiraComment.URL = fmt.Sprintf("%s/browse/%s?focusedCommentId=%s",
		c.Config.JiraBaseURL, ticketID, jiraComment.ID)

	return jiraComment, nil
}

// GetLinkedTickets fetches tickets linked to the given ticket
func (c *Client) GetLinkedTickets(ticketID string) ([]internalModels.JiraLink, error) {
	// This functionality is already handled in GetTicket
	ticket, err := c.GetTicket(ticketID)
	if err != nil {
		return nil, err
	}

	return ticket.Links, nil
}