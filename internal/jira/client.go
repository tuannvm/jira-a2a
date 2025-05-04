package jira

import (
	"context"
	"fmt"
	"log"

	v2 "github.com/ctreminiom/go-atlassian/v2/jira/v2"
	"github.com/ctreminiom/go-atlassian/v2/pkg/infra/models"
	"github.com/tuannvm/jira-a2a/internal/config"
)

// Client represents a Jira API client
type Client struct {
	Config     *config.Config
	JiraClient *v2.Client
	Ctx        context.Context
}

// ClientJiraTicket represents a Jira ticket in the client
type ClientJiraTicket struct {
	ID          string                 `json:"id"`
	Key         string                 `json:"key"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Fields      map[string]interface{} `json:"fields"`
	Links       []ClientJiraLink       `json:"links,omitempty"`
	DueDate     string                 `json:"dueDate,omitempty"`
}

// ClientJiraLink represents a link between Jira tickets
type ClientJiraLink struct {
	Type         string `json:"type"`
	InwardIssue  string `json:"inwardIssue,omitempty"`
	OutwardIssue string `json:"outwardIssue,omitempty"`
}

// ClientJiraComment represents a comment on a Jira ticket
type ClientJiraComment struct {
	ID      string `json:"id,omitempty"`
	Body    string `json:"body"`
	Created string `json:"created,omitempty"`
	Author  string `json:"author,omitempty"`
	URL     string `json:"url,omitempty"`
}

// NewClient creates a new Jira client
func NewClient(cfg *config.Config) *Client {
	// Create a background context
	ctx := context.Background()

	// Initialize the Jira client
	jiraClient, err := v2.New(nil, cfg.JiraBaseURL)
	if err != nil {
		log.Printf("Error initializing Jira client: %v", err)
		return &Client{
			Config: cfg,
			Ctx:    ctx,
		}
	}

	// Set authentication
	jiraClient.Auth.SetBasicAuth(cfg.JiraUsername, cfg.JiraAPIToken)

	// Create client instance
	c := &Client{
		Config:     cfg,
		JiraClient: jiraClient,
		Ctx:        ctx,
	}

	// Verify credentials by making a simple API call
	// Pass empty expand options as second parameter
	_, resp, err := jiraClient.MySelf.Details(ctx, []string{})
	if err != nil || (resp != nil && resp.StatusCode >= 400) {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		log.Printf("Warning: Failed to verify Jira credentials: %v (Status: %d)", err, statusCode)
	} else {
		log.Printf("Successfully connected to Jira API at %s", cfg.JiraBaseURL)
	}

	return c
}

// GetTicket fetches a Jira ticket by its ID
func (c *Client) GetTicket(ticketID string) (*ClientJiraTicket, error) {
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
	ticket := &ClientJiraTicket{
		ID:          issue.ID,
		Key:         issue.Key,
		Summary:     issue.Fields.Summary,
		Description: issue.Fields.Description,
		Fields:      make(map[string]interface{}),
		Links:       []ClientJiraLink{},
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
			jiraLink := ClientJiraLink{
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
func (c *Client) PostComment(ticketID, commentText string) (*ClientJiraComment, error) {
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
	jiraComment := &ClientJiraComment{
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
func (c *Client) GetLinkedTickets(ticketID string) ([]ClientJiraLink, error) {
	if c.JiraClient == nil {
		return nil, fmt.Errorf("jira client not initialized")
	}

	// This functionality is already handled in GetTicket
	ticket, err := c.GetTicket(ticketID)
	if err != nil {
		return nil, fmt.Errorf("failed to get linked tickets: %w", err)
	}

	return ticket.Links, nil
}
