package jira

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JiraWebhookPayload represents the standard Jira webhook payload structure
type JiraWebhookPayload struct {
	ID           int          `json:"id"`
	Timestamp    int64        `json:"timestamp"`
	Issue        JiraIssue    `json:"issue"`
	User         JiraUser     `json:"user"`
	Changelog    *Changelog   `json:"changelog,omitempty"`
	Comment      *JiraComment `json:"comment,omitempty"`
	WebhookEvent string       `json:"webhookEvent"`
}

// JiraIssue represents a Jira issue in the webhook
type JiraIssue struct {
	ID     string                 `json:"id"`
	Self   string                 `json:"self"`
	Key    string                 `json:"key"`
	Fields map[string]interface{} `json:"fields"`
}

// JiraUser represents a Jira user in the webhook
type JiraUser struct {
	Self         string            `json:"self"`
	Name         string            `json:"name"`
	Key          string            `json:"key"`
	EmailAddress string            `json:"emailAddress"`
	AvatarURLs   map[string]string `json:"avatarUrls"`
	DisplayName  string            `json:"displayName"`
	Active       interface{}       `json:"active"` // Can be string "true" or boolean true
}

// Changelog represents changes made in a Jira issue update
type Changelog struct {
	ID    int             `json:"id"`
	Items []ChangelogItem `json:"items"`
}

// ChangelogItem represents a single change in a Jira changelog
type ChangelogItem struct {
	Field      string `json:"field"`
	Fieldtype  string `json:"fieldtype"`
	From       string `json:"from"`
	FromString string `json:"fromString"`
	To         string `json:"to"`
	ToString   string `json:"toString"`
}

// JiraComment represents a comment in the Jira webhook
type JiraComment struct {
	ID           string   `json:"id"`
	Self         string   `json:"self"`
	Body         string   `json:"body"`
	Author       JiraUser `json:"author"`
	UpdateAuthor JiraUser `json:"updateAuthor"`
	Created      string   `json:"created"`
	Updated      string   `json:"updated"`
}

// TransformJiraWebhook converts a standard Jira webhook payload to the internal WebhookRequest format
func TransformJiraWebhook(payload []byte) (*WebhookRequest, error) {
	var jiraWebhook JiraWebhookPayload
	if err := json.Unmarshal(payload, &jiraWebhook); err != nil {
		return nil, err
	}

	// Create the WebhookRequest
	webhookReq := &WebhookRequest{
		TicketID:  jiraWebhook.Issue.Key,
		Event:     getEventTypeFromWebhookEvent(jiraWebhook.WebhookEvent),
		UserName:  jiraWebhook.User.Name,
		UserEmail: jiraWebhook.User.EmailAddress,
	}

	// Extract project key from ticket key (e.g., "JRA" from "JRA-20002")
	if parts := strings.Split(jiraWebhook.Issue.Key, "-"); len(parts) > 0 {
		webhookReq.ProjectKey = parts[0]
	}

	// Format timestamp
	if jiraWebhook.Timestamp > 0 {
		webhookReq.Timestamp = time.Unix(jiraWebhook.Timestamp/1000, 0).Format(time.RFC3339)
	} else {
		webhookReq.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Extract webhook name from event type
	webhookReq.WebhookName = jiraWebhook.WebhookEvent

	// Extract changes if present
	if jiraWebhook.Changelog != nil && len(jiraWebhook.Changelog.Items) > 0 {
		webhookReq.Changes = make(map[string]string)
		for _, item := range jiraWebhook.Changelog.Items {
			webhookReq.Changes[item.Field] = item.ToString
		}
	}

	// Extract relevant fields from issue
	if len(jiraWebhook.Issue.Fields) > 0 {
		webhookReq.CustomFields = make(map[string]string)
		for field, value := range jiraWebhook.Issue.Fields {
			// Handle different types of values
			switch v := value.(type) {
			case string:
				webhookReq.CustomFields[field] = v
			case float64, int, bool:
				webhookReq.CustomFields[field] = fmt.Sprintf("%v", v)
			default:
				// For complex types, convert to JSON string
				if jsonValue, err := json.Marshal(v); err == nil {
					webhookReq.CustomFields[field] = string(jsonValue)
				}
			}
		}
	}

	return webhookReq, nil
}

// getEventTypeFromWebhookEvent extracts the simplified event type from the full webhook event
func getEventTypeFromWebhookEvent(webhookEvent string) string {
	switch webhookEvent {
	case "jira:issue_created":
		return "created"
	case "jira:issue_updated":
		return "updated"
	case "jira:issue_commented":
		return "commented"
	case "jira:issue_deleted":
		return "deleted"
	default:
		// Extract event name after colon if present
		if parts := strings.Split(webhookEvent, ":"); len(parts) > 1 {
			return parts[1]
		}
		return webhookEvent
	}
}

// WebhookRequest represents the application's internal webhook request format
// This is the format used throughout the application for webhook processing
type WebhookRequest struct {
	TicketID     string            `json:"ticketId"`
	Event        string            `json:"event"`                  // "created", "updated", "commented", etc.
	UserName     string            `json:"userName"`               // The user who triggered the event
	UserEmail    string            `json:"userEmail"`              // The email of the user who triggered the event
	ProjectKey   string            `json:"projectKey"`             // The key of the project containing the issue
	Changes      map[string]string `json:"changes"`                // Map of fields that were changed and their new values
	WebhookName  string            `json:"webhookName"`            // Name of the webhook that was triggered
	Timestamp    string            `json:"timestamp"`              // When the webhook was triggered
	CustomFields map[string]string `json:"customFields,omitempty"` // Any custom fields from Jira
}