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
	config     *config.Config
	jiraClient *v2.Client
	ctx        context.Context
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
		config:     cfg,
		jiraClient: jiraClient,
		ctx:        ctx,
	}
}

// GetTicket fetches a Jira ticket by its ID
func (c *Client) GetTicket(ticketID string) (*internalModels.JiraTicket, error) {
	if c.jiraClient == nil {
		return nil, fmt.Errorf("jira client not initialized")
	}
	
	// Define fields to retrieve and expand options
	fields := []string{"summary", "description", "duedate", "issuelinks"}
	expand := []string{} // No expansion needed for now
	
	// Fetch the issue with relevant fields
	issue, response, err := c.jiraClient.Issue.Get(c.ctx, ticketID, fields, expand)
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
	if c.jiraClient == nil {
		return nil, fmt.Errorf("jira client not initialized")
	}

	// Create the comment payload for v2
	commentPayload := &models.CommentPayloadSchemeV2{
		Body: commentText,
	}

	// Post the comment to the issue using the v2 method
	responseComment, response, err := c.jiraClient.Issue.Comment.Add(c.ctx, ticketID, commentPayload, nil)
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
		c.config.JiraBaseURL, ticketID, jiraComment.ID)

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