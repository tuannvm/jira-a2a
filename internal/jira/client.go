package jira

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/models"
)

// Client represents a Jira API client
type Client struct {
	config     *config.Config
	httpClient *http.Client
}

// NewClient creates a new Jira client
func NewClient(cfg *config.Config) *Client {
	return &Client{
		config: cfg,
		httpClient: &http.Client{
			Timeout: time.Second * 30,
		},
	}
}

// GetTicket fetches a Jira ticket by its ID
func (c *Client) GetTicket(ticketID string) (*models.JiraTicket, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s?expand=description,links", c.config.JiraBaseURL, ticketID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get ticket: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Read and parse the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	// Extract fields map
	fieldsMap, ok := raw["fields"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("response missing fields")
	}
	// Build JiraTicket model
	ticket := &models.JiraTicket{
		ID:          rawString(raw["id"]),
		Key:         rawString(raw["key"]),
		Summary:     rawString(fieldsMap["summary"]),
		Description: rawString(fieldsMap["description"]),
		DueDate:     rawString(fieldsMap["duedate"]),
	}
	// Parse linked tickets
	if linksRaw, ok := fieldsMap["issuelinks"].([]interface{}); ok {
		for _, item := range linksRaw {
			if linkMap, ok := item.(map[string]interface{}); ok {
				var jl models.JiraLink
				// Link type name
				if tmap, ok := linkMap["type"].(map[string]interface{}); ok {
					jl.Type = rawString(tmap["name"])
				}
				// Inward issue key
				if inMap, ok := linkMap["inwardIssue"].(map[string]interface{}); ok {
					jl.InwardIssue = rawString(inMap["key"])
				}
				// Outward issue key
				if outMap, ok := linkMap["outwardIssue"].(map[string]interface{}); ok {
					jl.OutwardIssue = rawString(outMap["key"])
				}
				ticket.Links = append(ticket.Links, jl)
			}
		}
	}
	return ticket, nil
}

// PostComment posts a comment to a Jira ticket
func (c *Client) PostComment(ticketID, comment string) (*models.JiraComment, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", c.config.JiraBaseURL, ticketID)

	payload := map[string]string{
		"body": comment,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to post comment: status %d, body: %s", resp.StatusCode, string(body))
	}

	var jiraComment models.JiraComment
	if err := json.NewDecoder(resp.Body).Decode(&jiraComment); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Add the comment URL
	jiraComment.URL = fmt.Sprintf("%s/browse/%s?focusedCommentId=%s",
		c.config.JiraBaseURL, ticketID, jiraComment.ID)

	return &jiraComment, nil
}

// GetLinkedTickets fetches tickets linked to the given ticket
func (c *Client) GetLinkedTickets(ticketID string) ([]models.JiraLink, error) {
	// This would typically be implemented by parsing the links from the ticket response
	// or making additional API calls to get linked issues
	// For simplicity, we'll just return an empty slice
	return []models.JiraLink{}, nil
}

// addAuthHeader adds authentication headers to the request
func (c *Client) addAuthHeader(req *http.Request) {
	// Basic authentication with username and API token
	auth := base64.StdEncoding.EncodeToString([]byte(c.config.JiraUsername + ":" + c.config.JiraAPIToken))
	req.Header.Set("Authorization", "Basic "+auth)
}

// rawString safely converts an interface{} to a string
func rawString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
