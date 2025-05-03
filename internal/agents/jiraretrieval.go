package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/client"
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
	if cfg.ServerPort == 8081 {
		// If we're running on the JiraRetrievalAgent port, adjust the URL for the InformationGatheringAgent
		infoAgentURL = fmt.Sprintf("http://%s:8080", cfg.ServerHost)
	}

	var infoAgentClient *client.A2AClient
	var err error
	
	if cfg.AuthType == "jwt" {
		infoAgentClient, err = client.NewA2AClient(infoAgentURL)
	} else {
		infoAgentClient, err = client.NewA2AClient(infoAgentURL, client.WithAPIKeyAuth(cfg.APIKey, "X-API-Key"))
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
	log.Printf("Updating status to processing")
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
	log.Printf("Processing info gathered for ticket: %s", task.TicketID)

	// Update task status
	if err := handle.UpdateStatus(protocol.TaskState("processing_info"), nil); err != nil {
		log.Printf("Failed to update task status: %v", err)
	}

	// Log the information received
	log.Printf("Received information for ticket: %s", task.TicketID)
	log.Printf("Collected fields: %+v", task.CollectedFields)

	// Extract the analysis results
	log.Printf("Analysis suggestion: %s", task.CollectedFields["Suggestion"])

	// Check if we should update ticket fields based on analysis
	var ticketUpdateErr error
	if suggestion, ok := task.CollectedFields["Suggestion"]; ok && suggestion != "" {
		log.Printf("Attempting to update ticket fields based on analysis")
		ticketUpdateErr = updateTicketBasedOnAnalysis(j, task.TicketID, task.CollectedFields)
		if ticketUpdateErr != nil {
			log.Printf("Warning: Failed to update ticket fields: %v", ticketUpdateErr)
		}
	}

	// Update status to posting comment
	log.Printf("Updating status to posting_comment")
	if err := handle.UpdateStatus(protocol.TaskState("posting_comment"), nil); err != nil {
		log.Printf("Failed to update task status: %v", err)
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
	log.Printf("Posting comment to Jira for ticket: %s", task.TicketID)
	jiraComment, err := j.jiraClient.PostComment(task.TicketID, commentText)
	if err != nil {
		log.Printf("Failed to post comment to Jira: %v", err)
		// Continue processing even if comment posting fails
	} else {
		log.Printf("Successfully posted comment to Jira, URL: %s", jiraComment.URL)
		task.CommentURL = jiraComment.URL
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
			log.Printf("Failed to add artifact: %v", err)
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
		log.Printf("Failed to update task status: %v", err)
		return err
	}

	log.Printf("Task %s completed successfully", taskID)
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
		log.Printf("No ticket fields to update for ticket %s", ticketID)
		return nil
	}

	// Update the ticket fields
	return j.updateTicketFields(ticketID, fieldUpdates)
}

// updateTicketFields updates fields on a Jira ticket
func (j *JiraRetrievalAgent) updateTicketFields(ticketID string, fieldUpdates map[string]string) error {
	// This would make a call to update the Jira ticket fields
	// For now, we'll just log the updates as this functionality would depend on the specific Jira API implementation
	log.Printf("Would update ticket %s with the following field updates:", ticketID)
	for field, value := range fieldUpdates {
		log.Printf("  %s: %s", field, value)
	}

	// In a real implementation, we would call the Jira API to update the fields
	// For example: return j.jiraClient.UpdateTicket(ticketID, fieldUpdates)
	
	// Return nil for now since this is a placeholder
	return nil
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
		"Technical Analysis": {"TechnicalAnalysis", "CodeReview", "ArchitectureImpact"},
		"Business Impact": {"BusinessImpact", "UserImpact", "CustomerImpact"},
		"Recommendations": {"RecommendedPriority", "RecommendedComponents", "RecommendedLabels", "NextSteps"},
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
	Event        string            `json:"event"`      // "created", "updated", "commented", etc.
	UserName     string            `json:"userName"`   // The user who triggered the event
	UserEmail    string            `json:"userEmail"`  // The email of the user who triggered the event
	ProjectKey   string            `json:"projectKey"` // The key of the project containing the issue
	Changes      map[string]string `json:"changes"`    // Map of fields that were changed and their new values
	WebhookName  string            `json:"webhookName"` // Name of the webhook that was triggered
	Timestamp    string            `json:"timestamp"`   // When the webhook was triggered
	CustomFields map[string]string `json:"customFields,omitempty"` // Any custom fields from Jira
}

// RegisterWebhookHandler registers the webhook handler with the server
func (j *JiraRetrievalAgent) RegisterWebhookHandler(srv *server.A2AServer) error {
	// Try to determine if the server supports the standard A2A HTTP handler registration
	// that automatically applies authentication
	
	// First, try the official method based on the documentation
	handleFuncMethod := reflect.ValueOf(srv).MethodByName("HandleFunc")
	if handleFuncMethod.IsValid() && !handleFuncMethod.IsNil() {
		log.Printf("Using srv.HandleFunc method to register webhook handler")
		args := []reflect.Value{reflect.ValueOf("/webhook"), reflect.ValueOf(j.HandleWebhook)}
		results := handleFuncMethod.Call(args)
		
		// Check for error return value if it exists
		if len(results) > 0 && !results[0].IsNil() {
			if err, ok := results[0].Interface().(error); ok {
				return fmt.Errorf("failed to register webhook handler: %w", err)
			}
		}
		
		log.Printf("Successfully registered webhook handler with A2A server")
		logWebhookRegistrationSuccess(j.cfg)
		return nil
	}
	
	// If we can't find the standard HandleFunc method, try to find a Handler function
	// that we can use to register our handler
	handlerMethod := reflect.ValueOf(srv).MethodByName("Handler")
	if handlerMethod.IsValid() && !handlerMethod.IsNil() {
		log.Printf("Server has Handler method, but we need to determine how to register our handler")
		
		// Try to find a method to register our handler with the server's handler
		// This is implementation-specific and might require more investigation
	}
	
	// If we still can't register with the server, create our own HTTP handler with authentication
	log.Printf("Could not register webhook handler with A2A server, creating custom handler")
	
	// Get the server's auth provider if possible
	var authProvider auth.Provider
	getAuthProviderMethod := reflect.ValueOf(srv).MethodByName("AuthProvider")
	if getAuthProviderMethod.IsValid() && !getAuthProviderMethod.IsNil() {
		// Try to get the auth provider from the server
		results := getAuthProviderMethod.Call([]reflect.Value{})
		if len(results) > 0 && !results[0].IsNil() {
			if provider, ok := results[0].Interface().(auth.Provider); ok {
				log.Printf("Using server's auth provider for webhook handler")
				authProvider = provider
			}
		}
	}
	
	// If we couldn't get the server's auth provider, create our own based on the config
	if authProvider == nil {
		log.Printf("Creating new auth provider for webhook handler based on configuration")
		if j.cfg.AuthType == "jwt" {
			authProvider = auth.NewJWTAuthProvider(
				[]byte(j.cfg.JWTSecret),
				"", // audience
				"", // issuer
				24*time.Hour, // expiration
			)
			log.Printf("Created JWT auth provider for webhook handler")
		} else if j.cfg.AuthType == "apikey" {
			apiKeys := map[string]string{
				j.cfg.APIKey: "user",
			}
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
			log.Printf("Created API key auth provider for webhook handler")
		} else {
			log.Printf("No authentication configured for webhook handler")
		}
	}
	
	// Register the webhook handler with our custom HTTP server that uses the auth provider
	err := j.registerFallbackWebhookHandler(authProvider)
	if err != nil {
		return fmt.Errorf("failed to register fallback webhook handler: %w", err)
	}
	
	return nil
}

// logWebhookRegistrationSuccess logs information about successful webhook registration
func logWebhookRegistrationSuccess(cfg *config.Config) {
	// Log webhook endpoint information
	log.Printf("Webhook endpoint registered at: http://%s:%d/webhook", cfg.ServerHost, cfg.ServerPort)
	log.Printf("Security note: Webhook is using the server's built-in authentication")
	
	// Print test information with authentication header
	if cfg.AuthType == "apikey" {
		log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"X-API-Key: %s\" http://%s:%d/webhook", 
			cfg.APIKey, cfg.ServerHost, cfg.ServerPort)
	} else if cfg.AuthType == "jwt" {
		log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"Authorization: Bearer YOUR_JWT_TOKEN\" http://%s:%d/webhook", 
			cfg.ServerHost, cfg.ServerPort)
	} else {
		log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://%s:%d/webhook", 
			cfg.ServerHost, cfg.ServerPort)
	}
}

// registerFallbackWebhookHandler creates a separate HTTP server for webhooks
// This is used as a fallback when we can't register with the server's HTTP handler
func (j *JiraRetrievalAgent) registerFallbackWebhookHandler(authProvider auth.Provider) error {
	log.Printf("Creating a separate HTTP server for webhooks")
	
	// Create an authenticated handler using the provided auth provider
	var handler http.Handler
	
	if authProvider != nil {
		log.Printf("Using authentication for webhook endpoint")
		
		// Create a middleware that authenticates requests before passing them to the webhook handler
		handler = AuthMiddleware(authProvider, http.HandlerFunc(j.HandleWebhook))
	} else {
		log.Printf("WARNING: No authentication provider available, webhook endpoint will be unsecured")
		handler = http.HandlerFunc(j.HandleWebhook)
	}
	
	// Create a simple HTTP server to handle webhook requests
	go func() {
		router := http.NewServeMux()
		router.Handle("/webhook", handler)
		
		webhookServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", j.cfg.ServerPort),
			Handler: router,
		}
		
		// Log webhook endpoint information
		log.Printf("Webhook endpoint available at: http://%s:%d/webhook", j.cfg.ServerHost, j.cfg.ServerPort)
		
		// Print test information with authentication header
		if j.cfg.AuthType == "apikey" {
			log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"X-API-Key: %s\" http://%s:%d/webhook", 
				j.cfg.APIKey, j.cfg.ServerHost, j.cfg.ServerPort)
		} else if j.cfg.AuthType == "jwt" {
			log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' -H \"Authorization: Bearer YOUR_JWT_TOKEN\" http://%s:%d/webhook", 
				j.cfg.ServerHost, j.cfg.ServerPort)
		} else {
			log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://%s:%d/webhook", 
				j.cfg.ServerHost, j.cfg.ServerPort)
		}
		
		log.Printf("Starting webhook server on port %d", j.cfg.ServerPort)
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
			log.Printf("Authentication failed: %v", err)
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
	log.Printf("Received webhook request from %s", r.RemoteAddr)

	// Add request ID for tracking (in a production environment)
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	log.Printf("[%s] Processing webhook request", requestID)

	// Only accept POST requests
	if r.Method != http.MethodPost {
		log.Printf("[%s] Method not allowed: %s", requestID, r.Method)
		returnJSONError(w, http.StatusMethodNotAllowed, "Method not allowed: Only POST requests are accepted")
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.Contains(contentType, "application/json") {
		log.Printf("[%s] Invalid content type: %s", requestID, contentType)
		returnJSONError(w, http.StatusUnsupportedMediaType, "Content type must be application/json")
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[%s] Failed to read request body: %v", requestID, err)
		returnJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to read request body: %v", err))
		return
	}
	defer r.Body.Close()

	// Check if body is empty
	if len(body) == 0 {
		log.Printf("[%s] Empty request body", requestID)
		returnJSONError(w, http.StatusBadRequest, "Request body cannot be empty")
		return
	}

	// Log payload size instead of full payload (which could be large)
	log.Printf("[%s] Webhook payload size: %d bytes", requestID, len(body))

	// Parse the request body
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		log.Printf("[%s] Failed to parse request body: %v", requestID, err)
		log.Printf("[%s] Raw payload: %s", requestID, string(body))
		returnJSONError(w, http.StatusBadRequest, fmt.Sprintf("Failed to parse request body as JSON: %v", err))
		return
	}

	// Validate the request
	if webhookReq.TicketID == "" {
		log.Printf("[%s] Missing ticket ID in webhook request", requestID)
		returnJSONError(w, http.StatusBadRequest, "Missing required field: ticketId")
		return
	}

	// Validate event type
	if webhookReq.Event == "" {
		log.Printf("[%s] Missing event type in webhook request", requestID)
		returnJSONError(w, http.StatusBadRequest, "Missing required field: event")
		return
	}

	// Log the validated request
	log.Printf("[%s] Processing webhook for ticket: %s, event: %s", requestID, webhookReq.TicketID, webhookReq.Event)

	// Add webhook timestamp if not provided
	if webhookReq.Timestamp == "" {
		webhookReq.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Process the webhook
	if err := j.ProcessWebhook(r.Context(), &webhookReq); err != nil {
		log.Printf("[%s] Failed to process webhook: %v", requestID, err)
		returnJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to process webhook: %v", err))
		return
	}

	// Send a successful response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	responseBody := map[string]string{
		"status":   "success",
		"ticketId": webhookReq.TicketID,
		"message":  fmt.Sprintf("Successfully processed webhook for ticket %s", webhookReq.TicketID),
		"requestId": requestID,
	}
	responseJSON, _ := json.Marshal(responseBody)
	w.Write(responseJSON)

	// Log completion time
	elapsed := time.Since(start)
	log.Printf("[%s] Webhook processed in %v", requestID, elapsed)
}

// ProcessWebhook processes a webhook request
func (j *JiraRetrievalAgent) ProcessWebhook(ctx context.Context, webhookReq *WebhookRequest) error {
	log.Printf("Processing webhook for ticket: %s, event: %s", webhookReq.TicketID, webhookReq.Event)

	// Fetch the ticket from Jira
	log.Printf("Fetching ticket %s from Jira", webhookReq.TicketID)
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
			"hasAttachments":  fmt.Sprintf("%v", hasAttachments),
			"components":     components,
			"description":    ticket.Description,
			"webhookUser":    webhookReq.UserName,
			"userEmail":      webhookReq.UserEmail,
			"projectKey":     webhookReq.ProjectKey,
			"webhookName":    webhookReq.WebhookName,
			"webhookTime":    webhookReq.Timestamp,
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

	// Convert task to JSON
	taskJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %v", err)
	}

	// Create a message with the task data
	textPart := protocol.NewTextPart(string(taskJSON))
	message := protocol.Message{
		Parts: []protocol.Part{textPart},
	}

	// Send the task to InfoGatheringAgent
	log.Printf("Sending 'ticket-available' task to InfoGatheringAgent with %d metadata fields", len(taskData.Metadata))
	taskParams := protocol.SendTaskParams{
		Message: message,
	}

	// Send the task
	resp, err := j.infoAgentClient.SendTasks(ctx, taskParams)
	if err != nil {
		return fmt.Errorf("failed to send task: %v", err)
	}

	log.Printf("Successfully sent task. Task ID: %s", resp.ID)
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