package models

// TicketAvailableTask represents the data sent from JiraRetrievalAgent
// to InformationGatheringAgent when a relevant Jira ticket event occurs.
type TicketAvailableTask struct {
	TicketID    string            `json:"ticketId"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	Reporter    string            `json:"reporter"`
	Assignee    string            `json:"assignee"` // Assuming string for simplicity, might be complex type
	Priority    string            `json:"priority"`
	Labels      []string          `json:"labels"`
	Created     string            `json:"created"` // ISO 8601 format string
	Updated     string            `json:"updated"` // ISO 8601 format string
	Changes     string            `json:"changes"` // Description of recent changes
	Metadata    map[string]string `json:"metadata,omitempty"` // Optional additional fields
}

// InfoGatheredTask represents the result sent back from InformationGatheringAgent
// after processing a TicketAvailableTask.
type InfoGatheredTask struct {
	TaskID         string            `json:"taskId"`         // Original task ID
	TicketID       string            `json:"ticketId"`       // Jira Ticket ID
	AnalysisResult map[string]string `json:"analysisResult"` // Structured analysis from LLM or rules
	Summary        string            `json:"summary"`        // Human-readable summary
}
