package jira

import (
	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/models"
)

// JiraClientInterface defines the operations a Jira client should implement
type JiraClientInterface interface {
	GetTicket(ticketID string) (*models.JiraTicket, error)
	PostComment(ticketID, comment string) (*models.JiraComment, error)
	GetLinkedTickets(ticketID string) ([]models.JiraLink, error)
}

// NewAtlassianClient creates a new Jira client based on go-atlassian
func NewAtlassianClient(cfg *config.Config) JiraClientInterface {
	return NewClient(cfg)
}