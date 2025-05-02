package models

// TicketAvailableTask represents the data structure for a "ticket-available" task
type TicketAvailableTask struct {
	TicketID string            `json:"ticketId"`
	Summary  string            `json:"summary"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// InfoGatheredTask represents the data structure for an "info-gathered" task
type InfoGatheredTask struct {
	TicketID        string            `json:"ticketId"`
	CollectedFields map[string]string `json:"collectedFields"`
	CommentURL      string            `json:"commentUrl"`
}

// JiraTicket represents the data structure for a Jira ticket
type JiraTicket struct {
	ID          string                 `json:"id"`
	Key         string                 `json:"key"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Fields      map[string]interface{} `json:"fields"`
	Links       []JiraLink             `json:"links,omitempty"`
	DueDate     string                 `json:"dueDate,omitempty"`
}

// JiraLink represents a link between Jira tickets
type JiraLink struct {
	Type         string `json:"type"`
	InwardIssue  string `json:"inwardIssue,omitempty"`
	OutwardIssue string `json:"outwardIssue,omitempty"`
}

// JiraComment represents a comment on a Jira ticket
type JiraComment struct {
	ID      string `json:"id,omitempty"`
	Body    string `json:"body"`
	Created string `json:"created,omitempty"`
	Author  string `json:"author,omitempty"`
	URL     string `json:"url,omitempty"`
}
