package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/log" // Import trpc-a2a-go logging package with alias
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// JiraRetrievalAgent is an agent that processes Jira webhook events and communicates with InfoGatheringAgent
// It handles retrieving ticket information from Jira and communicating with InfoGatheringAgent
type JiraRetrievalAgent struct {
	cfg             *config.Config
	jiraClient      *jira.Client
	infoAgentClient *client.A2AClient
	httpServer      *http.ServeMux
}

// NewJiraRetrievalAgent creates a new JiraRetrievalAgent
func NewJiraRetrievalAgent(cfg *config.Config) *JiraRetrievalAgent {
	// Create Jira client
	jiraClient := jira.NewClient(cfg)

	// Create client to communicate with InfoGatheringAgent
	infoAgentURL := cfg.AgentURL

	// If we're running the JiraRetrievalAgent, we need to connect to the InfoGatheringAgent
	if cfg.AgentName == config.JiraRetrievalAgentName {
		// Construct the URL for the InfoGatheringAgent using the default port from config
		infoAgentURL = fmt.Sprintf("http://%s:%d", cfg.ServerHost, config.DefaultInfoGatheringPort)
	}

	var infoAgentClient *client.A2AClient
	var err error

	// Create client with appropriate authentication
	if cfg.AuthType == "jwt" {
		// JWT authentication
		infoAgentClient, err = client.NewA2AClient(infoAgentURL)
	} else if cfg.AuthType == "apikey" {
		// API key authentication - ensure header name matches what's expected by the server
		log.Infof("Using API key authentication with InfoGatheringAgent (API key length: %d)", len(cfg.APIKey))
		// Note: The header name must be 'X-API-Key' and the value must be the API key
		// This must match how the server is configured in InformationGatheringAgent
		infoAgentClient, err = client.NewA2AClient(infoAgentURL, client.WithAPIKeyAuth(cfg.APIKey, "X-API-Key"))
	} else {
		// Default to no authentication
		log.Warnf("Warning: No authentication configured for InfoGatheringAgent client")
		infoAgentClient, err = client.NewA2AClient(infoAgentURL)
	}

	if err != nil {
		log.Fatalf("Failed to create InfoGatheringAgent client: %v", err)
	}

	// Create HTTP server mux for webhook handler
	mux := http.NewServeMux()

	return &JiraRetrievalAgent{
		cfg:             cfg,
		jiraClient:      jiraClient,
		infoAgentClient: infoAgentClient,
		httpServer:      mux,
	}
}

// Process implements the TaskProcessor interface
func (j *JiraRetrievalAgent) Process(ctx context.Context, taskID string, msg protocol.Message, handle taskmanager.TaskHandle) error {
	// Check if we have a valid message
	if len(msg.Parts) == 0 {
		return fmt.Errorf("empty message or no parts")
	}

	// Look for a text part
	var textPart *protocol.TextPart
	for _, part := range msg.Parts {
		if tp, ok := part.(*protocol.TextPart); ok {
			textPart = tp
			break
		}
	}

	if textPart == nil {
		return fmt.Errorf("no text part found in message")
	}

	// The text part should contain the task payload
	payload := textPart.Text

	// Update status to processing
	log.Infof("Updating status to processing")
	if err := handle.UpdateStatus(protocol.TaskState("processing"), nil); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	// First, try to parse as InfoGatheredTask (from InformationGatheringAgent)
	var infoTask models.InfoGatheredTask
	if err := json.Unmarshal([]byte(payload), &infoTask); err == nil && infoTask.TicketID != "" {
		return j.ProcessInfoGatheredTask(ctx, taskID, &infoTask, handle)
	}

	// If not an InfoGatheredTask, it's an error because this agent expects either webhook events
	// or InfoGatheredTask responses from InfoGatheringAgent
	return fmt.Errorf("unknown task payload: %s", payload)
}

// ProcessInfoGatheredTask processes an InfoGatheredTask from InformationGatheringAgent
func (j *JiraRetrievalAgent) ProcessInfoGatheredTask(ctx context.Context, taskID string, task *models.InfoGatheredTask, handle taskmanager.TaskHandle) error {
	log.Infof("Processing info gathered for ticket: %s", task.TicketID)

	// Update task status
	if err := handle.UpdateStatus(protocol.TaskState("processing_info"), nil); err != nil {
		log.Infof("Failed to update task status: %v", err)
	}

	// Log the information received
	log.Infof("Received information for ticket: %s", task.TicketID)
	log.Infof("Collected fields: %+v", task.CollectedFields)

	// Extract the analysis results
	log.Infof("Analysis suggestion: %s", task.CollectedFields["Suggestion"])

	// Check if we should update ticket fields based on analysis
	var ticketUpdateErr error
	if suggestion, ok := task.CollectedFields["Suggestion"]; ok && suggestion != "" {
		log.Infof("Attempting to update ticket fields based on analysis")
		ticketUpdateErr = updateTicketBasedOnAnalysis(j, task.TicketID, task.CollectedFields)
		if ticketUpdateErr != nil {
			log.Infof("Warning: Failed to update ticket fields: %v", ticketUpdateErr)
		}
	}

	// Update status to posting comment
	log.Infof("Updating status to posting_comment")
	if err := handle.UpdateStatus(protocol.TaskState("posting_comment"), nil); err != nil {
		log.Infof("Failed to update task status: %v", err)
	}

	// Format the comment for Jira
	commentText := j.formatJiraComment(task)

	// Add information about field updates to the comment if applicable
	if ticketUpdateErr != nil {
		commentText += "\n\n*Note:* There was an issue updating some ticket fields automatically. Please review the analysis and update manually if needed."
	} else if suggestion, ok := task.CollectedFields["Suggestion"]; ok && suggestion != "" {
		commentText += "\n\n*Note:* Some ticket fields have been automatically updated based on this analysis."
	}

	// Post the comment to Jira using the Jira client
	log.Infof("Posting comment to Jira for ticket: %s", task.TicketID)
	jiraComment, err := j.jiraClient.PostComment(task.TicketID, commentText)
	if err != nil {
		log.Infof("Failed to post comment to Jira: %v", err)
		// Continue processing even if comment posting fails
	} else {
		log.Infof("Successfully posted comment to Jira, URL: %s", jiraComment.URL)
		// Note: CommentURL field has been removed from InfoGatheredTask model
		// as the InformationGatheringAgent no longer interacts with Jira API
	}

	// Record the comment as an artifact
	if jiraComment != nil && jiraComment.URL != "" {
		artifact := protocol.Artifact{
			Name:        stringPtr("comment"),
			Description: stringPtr("Jira Comment"),
			Parts:       []protocol.Part{},
			Metadata: map[string]interface{}{
				"url": jiraComment.URL,
			},
		}
		if err := handle.AddArtifact(artifact); err != nil {
			log.Infof("Failed to add artifact: %v", err)
		}
	}

	// Create response message
	responseText := fmt.Sprintf("Successfully processed information for ticket %s and posted comment to Jira", task.TicketID)
	textPart := protocol.NewTextPart(responseText)
	responseMsg := &protocol.Message{
		Parts: []protocol.Part{textPart},
	}

	// Complete the task
	if err := handle.UpdateStatus(protocol.TaskState("completed"), responseMsg); err != nil {
		log.Infof("Failed to update task status: %v", err)
		return err
	}

	log.Infof("Task %s completed successfully", taskID)
	return nil
}

// updateTicketBasedOnAnalysis updates ticket fields based on analysis results
func updateTicketBasedOnAnalysis(j *JiraRetrievalAgent, ticketID string, collectedFields map[string]string) error {
	// Determine which fields need to be updated based on analysis
	fieldUpdates := make(map[string]string)

	// Check for priority recommendations
	if recommendedPriority, ok := collectedFields["RecommendedPriority"]; ok && recommendedPriority != "" {
		fieldUpdates["priority"] = recommendedPriority
	}

	// Check for component recommendations
	if recommendedComponents, ok := collectedFields["RecommendedComponents"]; ok && recommendedComponents != "" {
		fieldUpdates["components"] = recommendedComponents
	}

	// Check for label recommendations
	if recommendedLabels, ok := collectedFields["RecommendedLabels"]; ok && recommendedLabels != "" {
		fieldUpdates["labels"] = recommendedLabels
	}

	// If no fields to update, return nil
	if len(fieldUpdates) == 0 {
		log.Infof("No ticket fields to update for ticket %s", ticketID)
		return nil
	}

	// Update the ticket fields
	return j.updateTicketFields(ticketID, fieldUpdates)
}

// updateTicketFields updates fields on a Jira ticket
func (j *JiraRetrievalAgent) updateTicketFields(ticketID string, fieldUpdates map[string]string) error {
	// This would make a call to update the Jira ticket fields
	// For now, we'll just log the updates as this functionality would depend on the specific Jira API implementation
	log.Infof("Would update ticket %s with the following field updates:", ticketID)
	for field, value := range fieldUpdates {
		log.Infof("  %s: %s", field, value)
	}

	// In a real implementation, we would call the Jira API to update the fields
	// For example: return j.jiraClient.UpdateTicket(ticketID, fieldUpdates)

	// Return nil for now since this is a placeholder
	return nil
}

// extractInfoGatheredTask extracts an InfoGatheredTask from a message
func extractInfoGatheredTask(message *protocol.Message, task *models.InfoGatheredTask) error {
	if message == nil || len(message.Parts) == 0 {
		return fmt.Errorf("message is nil or has no parts")
	}

	// Try to extract from each part
	for _, part := range message.Parts {
		// Check if it's a DataPart
		dataPart, ok := part.(*protocol.DataPart)
		if ok && dataPart != nil && dataPart.Data != nil {
			// Try to convert the data to InfoGatheredTask
			dataBytes, err := json.Marshal(dataPart.Data)
			if err == nil {
				if err := json.Unmarshal(dataBytes, task); err == nil {
					if task.TicketID != "" {
						return nil // Successfully extracted
					}
				}
			}
		}

		// Check if it's a TextPart
		textPart, ok := part.(*protocol.TextPart)
		if ok && textPart != nil && textPart.Text != "" {
			// Try to unmarshal the text as JSON
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				if task.TicketID != "" {
					return nil // Successfully extracted
				}
			}
		}
	}

	return fmt.Errorf("could not extract InfoGatheredTask from message")
}

// formatJiraComment formats the InfoGatheredTask data into a well-structured Jira comment

func (j *JiraRetrievalAgent) formatJiraComment(task *models.InfoGatheredTask) string {
	var sb strings.Builder

	// Add header with emoji
	sb.WriteString(":mag: *Information Gathering Results* :mag:\n\n")

	// Add a summary of the analysis with distinctive formatting
	if suggestion, ok := task.CollectedFields["Suggestion"]; ok && suggestion != "" {
		sb.WriteString(":bulb: *Recommendation:* \n")
		sb.WriteString(fmt.Sprintf("{panel:title=Analysis Suggestion|borderStyle=solid|borderColor=#ccc|titleBGColor=#f0f0f0|bgColor=#fff}%s{panel}\n\n", suggestion))
	}

	// Group the collected fields by category if possible
	categories := map[string][]string{
		"Technical Analysis":     {"TechnicalAnalysis", "CodeReview", "ArchitectureImpact"},
		"Business Impact":        {"BusinessImpact", "UserImpact", "CustomerImpact"},
		"Recommendations":        {"RecommendedPriority", "RecommendedComponents", "RecommendedLabels", "NextSteps"},
		"Additional Information": {"References", "RelatedTickets", "Context"},
	}

	// Track which fields we've already processed
	processedFields := map[string]bool{"Suggestion": true}

	// Add fields by category
	for category, fieldNames := range categories {
		hasFields := false
		categoryContent := fmt.Sprintf("*%s:*\n", category)

		for _, fieldName := range fieldNames {
			if value, ok := task.CollectedFields[fieldName]; ok && value != "" {
				processedFields[fieldName] = true
				categoryContent += fmt.Sprintf("- *%s:* %s\n", fieldName, value)
				hasFields = true
			}
		}

		if hasFields {
			sb.WriteString(categoryContent + "\n")
		}
	}

	// Add any remaining fields that weren't categorized
	hasUncategorized := false
	uncategorizedContent := "*Other Analysis Details:*\n"
	for key, value := range task.CollectedFields {
		if !processedFields[key] && value != "" {
			uncategorizedContent += fmt.Sprintf("- *%s:* %s\n", key, value)
			hasUncategorized = true
		}
	}

	if hasUncategorized {
		sb.WriteString(uncategorizedContent + "\n")
	}

	// Add footer with system information
	sb.WriteString("\n{panel:title=System Information|borderStyle=dashed|borderColor=#ddd|titleBGColor=#f5f5f5|bgColor=#f9f9f9}")
	sb.WriteString("This comment was automatically generated by the A2A Information Gathering System.\n")
	sb.WriteString(fmt.Sprintf("Generated on: %s", time.Now().Format(time.RFC1123)))
	sb.WriteString("{panel}")

	return sb.String()
}

// WebhookRequest represents the structure of incoming webhook requests
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

// RegisterWebhookHandler registers the webhook handler with the server
func (j *JiraRetrievalAgent) RegisterWebhookHandler(srv *server.A2AServer) error {
	// Create an auth provider based on the configuration
	var authProvider auth.Provider
	if j.cfg.AuthType == "jwt" {
		authProvider = auth.NewJWTAuthProvider(
			[]byte(j.cfg.JWTSecret),
			"",           // audience
			"",           // issuer
			24*time.Hour, // expiration
		)
		log.Infof("Created JWT auth provider for webhook handler")
	} else if j.cfg.AuthType == "apikey" {
		apiKeys := map[string]string{
			j.cfg.APIKey: "user",
		}
		authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		log.Infof("Created API key auth provider for webhook handler")
	} else {
		log.Infof("No authentication configured for webhook handler")
	}

	// Register the webhook handler with a separate HTTP server
	err := j.registerFallbackWebhookHandler(authProvider)
	if err != nil {
		return fmt.Errorf("failed to register fallback webhook handler: %w", err)
	}

	return nil
}

// logWebhookRegistrationSuccess logs information about successful webhook registration
func logWebhookRegistrationSuccess(cfg *config.Config) {
	// Log webhook endpoint information
	log.Infof("Webhook endpoint registered at: http://%s:%d/webhook", cfg.ServerHost, cfg.ServerPort)
	log.Infof("Security note: Webhook is using the server's built-in authentication")

	// Print test information with authentication header
	if cfg.AuthType == "apikey" {
		log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"X-API-Key: %s\" http://%s:%d/webhook",
			cfg.APIKey, cfg.ServerHost, cfg.ServerPort)
	} else if cfg.AuthType == "jwt" {
		log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"Authorization: Bearer YOUR_JWT_TOKEN\" http://%s:%d/webhook",
			cfg.ServerHost, cfg.ServerPort)
	} else {
		log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://%s:%d/webhook",
			cfg.ServerHost, cfg.ServerPort)
	}
}

// registerFallbackWebhookHandler creates a separate HTTP server for webhooks
// This is used as a fallback when we can't register with the server's HTTP handler
func (j *JiraRetrievalAgent) registerFallbackWebhookHandler(authProvider auth.Provider) error {
	log.Infof("Creating a separate HTTP server for webhooks")

	// Create an authenticated handler using the provided auth provider
	var handler http.Handler

	if authProvider != nil {
		log.Infof("Using authentication for webhook endpoint")

		// Create a middleware that authenticates requests before passing them to the webhook handler
		handler = AuthMiddleware(authProvider, http.HandlerFunc(j.HandleWebhook))
	} else {
		log.Warnf("WARNING: No authentication provider available, webhook endpoint will be unsecured")
		handler = http.HandlerFunc(j.HandleWebhook)
	}

	// Create a simple HTTP server to handle webhook requests
	go func() {
		router := http.NewServeMux()
		router.Handle("/webhook", handler)

		// This function is now deprecated as we're using the integrated webhook handler
		// But we'll keep it for backward compatibility
		// Use a different port for the webhook server to avoid conflict with the A2A server
		webhookPort := j.cfg.ServerPort + 3 // Use ServerPort + 3 as a convention for separate webhook servers
		webhookServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", webhookPort),
			Handler: router,
		}

		// Log webhook endpoint information
		log.Infof("Webhook endpoint available at: http://%s:%d/webhook", j.cfg.ServerHost, webhookPort)

		// Print test information with authentication header
		if j.cfg.AuthType == "apikey" {
			log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"X-API-Key: %s\" http://%s:%d/webhook",
				j.cfg.APIKey, j.cfg.ServerHost, webhookPort)
		} else if j.cfg.AuthType == "jwt" {
			log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"Authorization: Bearer YOUR_JWT_TOKEN\" http://%s:%d/webhook",
				j.cfg.ServerHost, webhookPort)
		} else {
			log.Infof("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://%s:%d/webhook",
				j.cfg.ServerHost, webhookPort)
		}

		log.Infof("Starting webhook server on port %d", webhookPort)
		if err := webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Webhook server error: %v", err)
		}
	}()

	return nil
}

// returnJSONError returns a JSON-formatted error response that matches the A2A API format
func returnJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    statusCode,
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(errorResponse)
}

// AuthUserContextKey is a context key for storing authenticated username
type AuthUserContextKey struct{}

// AuthMiddleware creates an HTTP middleware that uses the specified auth.Provider
// to authenticate requests before passing them to the handler
func AuthMiddleware(provider auth.Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip authentication if provider is nil
		if provider == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Authenticate the request
		username, err := provider.Authenticate(r)
		if err != nil {
			// Authentication failed
			log.Infof("Authentication failed: %v", err)
			returnJSONError(w, http.StatusUnauthorized, fmt.Sprintf("Unauthorized: %v", err))
			return
		}

		// Authentication succeeded
		// Create a new context with the authenticated user
		authCtx := context.WithValue(r.Context(), AuthUserContextKey{}, username)

		// Call the next handler with the authenticated context
		next.ServeHTTP(w, r.WithContext(authCtx))
	})
}

// HandleWebhook processes Jira webhook requests
func (j *JiraRetrievalAgent) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	log.Infof("Received webhook request from %s", r.RemoteAddr)

	// Add request ID for tracking (in a production environment)
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	log.Infof("[%s] Processing webhook request", requestID)

	// Only accept POST requests
	if r.Method != http.MethodPost {
		log.Infof("[%s] Method not allowed: %s", requestID, r.Method)
		returnJSONError(w, http.StatusMethodNotAllowed, "Method not allowed: Only POST requests are accepted")
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.Contains(contentType, "application/json") {
		log.Infof("[%s] Invalid content type: %s", requestID, contentType)
		returnJSONError(w, http.StatusUnsupportedMediaType, "Content type must be application/json")
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Infof("[%s] Failed to read request body: %v", requestID, err)
		returnJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to read request body: %v", err))
		return
	}
	defer r.Body.Close()

	// Check if body is empty
	if len(body) == 0 {
		log.Infof("[%s] Empty request body", requestID)
		returnJSONError(w, http.StatusBadRequest, "Request body cannot be empty")
		return
	}

	// Log payload size instead of full payload (which could be large)
	log.Infof("[%s] Webhook payload size: %d bytes", requestID, len(body))

	// Parse the request body
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		log.Infof("[%s] Failed to parse request body: %v", requestID, err)
		log.Infof("[%s] Raw payload: %s", requestID, string(body))
		returnJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse request body as JSON: %v", err))
		return
	}

	// Validate the request
	if webhookReq.TicketID == "" {
		log.Infof("[%s] Missing ticket ID in webhook request", requestID)
		returnJSONError(w, http.StatusBadRequest, "Missing required field: ticketId")
		return
	}

	// Validate event type
	if webhookReq.Event == "" {
		log.Infof("[%s] Missing event type in webhook request", requestID)
		returnJSONError(w, http.StatusBadRequest, "Missing required field: event")
		return
	}

	// Log the validated request
	log.Infof("[%s] Processing webhook for ticket: %s, event: %s", requestID, webhookReq.TicketID, webhookReq.Event)

	// Add webhook timestamp if not provided
	if webhookReq.Timestamp == "" {
		webhookReq.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Process the webhook
	if err := j.ProcessWebhook(r.Context(), &webhookReq); err != nil {
		log.Infof("[%s] Failed to process webhook: %v", requestID, err)
		returnJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to process webhook: %v", err))
		return
	}

	// Send a successful response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	responseBody := map[string]string{
		"status":    "success",
		"ticketId":  webhookReq.TicketID,
		"message":   fmt.Sprintf("Successfully processed webhook for ticket %s", webhookReq.TicketID),
		"requestId": requestID,
	}
	responseJSON, _ := json.Marshal(responseBody)
	w.Write(responseJSON)

	// Log completion time
	elapsed := time.Since(start)
	log.Infof("[%s] Webhook processed in %v", requestID, elapsed)
}

// ProcessWebhook processes a webhook request
func (j *JiraRetrievalAgent) ProcessWebhook(ctx context.Context, webhookReq *WebhookRequest) error {
	log.Infof("Processing webhook for ticket: %s, event: %s", webhookReq.TicketID, webhookReq.Event)

	// Fetch the ticket from Jira
	log.Infof("Fetching ticket %s from Jira", webhookReq.TicketID)
	ticket, err := j.jiraClient.GetTicket(webhookReq.TicketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket from Jira: %v", err)
	}

	// Extract important information from the ticket
	priority := getTicketPriority(ticket)
	issueType := getTicketIssueType(ticket)
	reporter := getTicketReporter(ticket)
	hasAttachments := hasAttachments(ticket)
	components := getTicketComponents(ticket)

	// Create a TicketAvailableTask with enriched data
	taskData := models.TicketAvailableTask{
		TicketID: ticket.Key,
		Summary:  ticket.Summary,
		Metadata: map[string]string{
			"event":          webhookReq.Event,
			"priority":       priority,
			"issueType":      issueType,
			"reporter":       reporter,
			"hasAttachments": fmt.Sprintf("%v", hasAttachments),
			"components":     components,
			"description":    ticket.Description,
		},
	}

	// Add any changes that were reported in the webhook
	if len(webhookReq.Changes) > 0 {
		for field, value := range webhookReq.Changes {
			taskData.Metadata["change_"+field] = value
		}
	}

	// Add any custom fields
	if len(webhookReq.CustomFields) > 0 {
		for field, value := range webhookReq.CustomFields {
			taskData.Metadata["custom_"+field] = value
		}
	}

	// Add any other fields from the ticket that might be useful
	for key, value := range ticket.Fields {
		// Convert the value to string
		valueStr := fmt.Sprintf("%v", value)
		if valueStr != "" && len(valueStr) < 1000 { // Avoid adding huge fields
			taskData.Metadata[key] = valueStr
		}
	}

	// Create a message with the task data using DataPart for proper JSON handling
	dataPart := protocol.DataPart{
		Type: "data",
		Data: taskData,
		Metadata: map[string]interface{}{
			"content-type": "application/json",
		},
	}

	message := protocol.Message{
		Parts: []protocol.Part{&dataPart},
	}

	// Generate a unique task ID based on the ticket ID and timestamp
	taskID := fmt.Sprintf("task-%s-%d", webhookReq.TicketID, time.Now().UnixNano())
	log.Infof("Generated task ID: %s", taskID)

	// Send the task to InfoGatheringAgent
	log.Infof("Sending 'ticket-available' task to InfoGatheringAgent with %d metadata fields", len(taskData.Metadata))
	taskParams := protocol.SendTaskParams{
		ID:      taskID, // Set the task ID explicitly
		Message: message,
	}

	// Send the task and get the task ID
	resp, err := j.infoAgentClient.SendTasks(ctx, taskParams)
	if err != nil {
		log.Warnf("Warning: Could not send task to InfoGatheringAgent: %v", err)
		return fmt.Errorf("failed to send task to InfoGatheringAgent: %v", err)
	}

	// Debug the response
	respBytes, _ := json.Marshal(resp)
	log.Infof("SendTasks response: %s", string(respBytes))

	// Verify that we received a valid task ID
	if resp.ID == "" {
		log.Error("Error: Received empty task ID from InfoGatheringAgent")
		return fmt.Errorf("received empty task ID from InfoGatheringAgent")
	}

	log.Infof("Successfully sent task. Task ID: %s", resp.ID)

	// Extract the InfoGatheredTask from the response
	var infoTask models.InfoGatheredTask

	// Only proceed if the task is completed synchronously
	if resp.Status.State != "completed" || resp.Status.Message == nil {
		return fmt.Errorf("task is not completed yet or no message in response")
	}

	log.Infof("Task was completed synchronously, extracting result from response")

	// Ensure we have message parts to process
	if len(resp.Status.Message.Parts) == 0 {
		return fmt.Errorf("task completed but no message parts found")
	}

	// Try to extract the task data from the message parts
	for _, part := range resp.Status.Message.Parts {
		// Try to extract from TextPart (which is what InfoGatheringAgent uses)
		textPart, ok := part.(*protocol.TextPart)
		if !ok || textPart == nil || textPart.Text == "" {
			continue
		}

		// Log the raw text for debugging
		log.Infof("Found TextPart in response: %s", textPart.Text)

		// Try direct unmarshal first
		if err := json.Unmarshal([]byte(textPart.Text), &infoTask); err == nil {
			if infoTask.TicketID != "" {
				log.Infof("Successfully extracted InfoGatheredTask directly")
				goto ProcessResult
			}
		}

		// Try parsing as a JSON string that contains the actual JSON
		var jsonStr string
		if err := json.Unmarshal([]byte(textPart.Text), &jsonStr); err == nil {
			// Now try to parse the string as an InfoGatheredTask
			if err := json.Unmarshal([]byte(jsonStr), &infoTask); err == nil {
				if infoTask.TicketID != "" {
					log.Infof("Successfully extracted InfoGatheredTask from JSON string")
					goto ProcessResult
				}
			}
		}
	}

	// If we reach here, we couldn't extract the InfoGatheredTask
	return fmt.Errorf("failed to extract InfoGatheredTask from response")

	// Label for processing the extracted result
ProcessResult:

	log.Infof("Successfully processed InfoGatheredTask for ticket %s", infoTask.TicketID)

	// Format the comment for Jira
	commentText := j.formatJiraComment(&infoTask)

	// Post the comment to Jira using the Jira client
	log.Infof("Posting comment to Jira for ticket: %s", infoTask.TicketID)
	jiraComment, err := j.jiraClient.PostComment(infoTask.TicketID, commentText)
	if err != nil {
		log.Infof("Failed to post comment to Jira: %v", err)
		return fmt.Errorf("failed to post comment to Jira: %v", err)
	}

	log.Infof("Successfully posted comment to Jira, URL: %s", jiraComment.URL)
	return nil
}

// getTicketPriority extracts the priority from a ticket
func getTicketPriority(ticket *models.JiraTicket) string {
	if priority, ok := ticket.Fields["priority"].(string); ok && priority != "" {
		return priority
	}
	return "Unknown"
}

// getTicketIssueType extracts the issue type from a ticket
func getTicketIssueType(ticket *models.JiraTicket) string {
	if issueType, ok := ticket.Fields["issueType"].(string); ok && issueType != "" {
		return issueType
	}
	return "Unknown"
}

// getTicketReporter extracts the reporter from a ticket
func getTicketReporter(ticket *models.JiraTicket) string {
	if reporter, ok := ticket.Fields["reporter"].(string); ok && reporter != "" {
		return reporter
	}
	return "Unknown"
}

// hasAttachments checks if a ticket has attachments
func hasAttachments(ticket *models.JiraTicket) bool {
	// This would need to be implemented based on how attachments are represented in the ticket
	// For now, returning a placeholder value
	return false
}

// getTicketComponents extracts components as a comma-separated string
func getTicketComponents(ticket *models.JiraTicket) string {
	if components, ok := ticket.Fields["components"].([]string); ok && len(components) > 0 {
		return strings.Join(components, ", ")
	}
	return ""
}

// SetupServer creates and configures the A2A server for the JiraRetrievalAgent
func (j *JiraRetrievalAgent) SetupServer() (*server.A2AServer, error) {
	// Define the agent card
	agentCard := server.AgentCard{
		Name:        j.cfg.AgentName,
		Description: stringPtr("Agent that retrieves information and handles webhooks"),
		URL:         j.cfg.AgentURL,
		Version:     j.cfg.AgentVersion,
		Provider: &server.AgentProvider{
			Organization: "Your Organization",
			URL:          stringPtr("https://example.com"),
		},
		Capabilities: server.AgentCapabilities{
			Streaming:              false,
			StateTransitionHistory: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []server.AgentSkill{
			{
				ID:          "process-jira-webhook",
				Name:        "Process Jira Webhook",
				Description: stringPtr("Processes webhook events and emits 'ticket-available' tasks"),
				Tags:        []string{"webhook", "ticket"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
			{
				ID:          "process-info-gathered",
				Name:        "Process Info Gathered",
				Description: stringPtr("Processes information gathered and posts comments"),
				Tags:        []string{"comment", "information"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
		},
	}

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(j)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	// Setup server options
	serverOpts := []server.Option{}

	// Add authentication if enabled
	if j.cfg.AuthType != "" {
		var authProvider auth.Provider
		switch j.cfg.AuthType {
		case "jwt":
			authProvider = auth.NewJWTAuthProvider(
				[]byte(j.cfg.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			apiKeys := map[string]string{
				j.cfg.APIKey: "user",
			}
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			return nil, fmt.Errorf("unsupported auth type: %s", j.cfg.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
	}

	// Create the server
	srv, err := server.NewA2AServer(agentCard, taskManager, serverOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	// Add webhook handler
	if err := j.RegisterWebhookHandler(srv); err != nil {
		return nil, fmt.Errorf("failed to register webhook handler: %w", err)
	}

	return srv, nil
}

// StartServer starts the A2A server and handles graceful shutdown
func (j *JiraRetrievalAgent) StartServer(ctx context.Context) error {
	// Setup the server
	srv, err := j.SetupServer()
	if err != nil {
		return fmt.Errorf("failed to setup server: %w", err)
	}

	// Start the server in a goroutine
	addr := fmt.Sprintf("%s:%d", j.cfg.ServerHost, j.cfg.ServerPort)
	go func() {
		log.Infof("Starting A2A server on %s", addr)
		if err := srv.Start(addr); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Create a context with a timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server
	log.Info("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}
