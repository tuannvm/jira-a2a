package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// JiraRetrievalAgent is an agent that processes Jira webhook events and communicates with InfoGatheringAgent
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
	log.Printf("Comment URL: %s", task.CommentURL)
	log.Printf("Collected fields: %+v", task.CollectedFields)

	// In a real implementation, you would:
	// 1. Update Jira ticket with the collected information
	// 2. Post additional comments if needed
	// 3. Trigger automation if required

	// Create response message
	responseText := fmt.Sprintf("Successfully processed information for ticket %s", task.TicketID)
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

// WebhookRequest represents the structure of incoming webhook requests
type WebhookRequest struct {
	TicketID string `json:"ticketId"`
	Event    string `json:"event"` // "created", "updated", "commented", etc.
	// Additional fields based on your Jira webhook structure
}

// RegisterWebhookHandler registers the webhook handler with the server
func (j *JiraRetrievalAgent) RegisterWebhookHandler(srv *server.A2AServer) error {
	// In a production environment, you would need to expose a webhook endpoint
	// For this implementation, we'll just add a handler to the server's HTTPServer mux
	// This is a simplified approach for demonstration purposes
	
	// We would typically use srv.HTTPServer() to get the HTTP server
	// Since this method doesn't exist, we'll create a separate HTTP server
	// in a production environment
	
	log.Printf("The webhook endpoint should be available at: http://%s:%d/webhook", j.cfg.ServerHost, j.cfg.ServerPort)
	log.Printf("You can test it with: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://%s:%d/webhook", j.cfg.ServerHost, j.cfg.ServerPort)
	
	// Create a simple HTTP server to handle webhook requests
	go func() {
		router := http.NewServeMux()
		router.HandleFunc("/webhook", j.HandleWebhook)
		
		webhookServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", j.cfg.ServerPort),
			Handler: router,
		}
		
		log.Printf("Starting webhook server on port %d", j.cfg.ServerPort)
		if err := webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Webhook server error: %v", err)
		}
	}()
	
	return nil
}

// HandleWebhook processes Jira webhook requests
func (j *JiraRetrievalAgent) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received webhook request from %s", r.RemoteAddr)

	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Debug the request
	log.Printf("Webhook payload: %s", string(body))

	// Parse the request body
	var webhookReq WebhookRequest
	if err := json.Unmarshal(body, &webhookReq); err != nil {
		log.Printf("Failed to parse request body: %v", err)
		http.Error(w, "Failed to parse request body", http.StatusBadRequest)
		return
	}

	// Validate the request
	if webhookReq.TicketID == "" {
		log.Printf("Missing ticket ID in webhook request")
		http.Error(w, "Missing ticket ID", http.StatusBadRequest)
		return
	}

	// Process the webhook
	if err := j.ProcessWebhook(r.Context(), &webhookReq); err != nil {
		log.Printf("Failed to process webhook: %v", err)
		http.Error(w, "Failed to process webhook", http.StatusInternalServerError)
		return
	}

	// Send a successful response
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Successfully processed webhook for ticket %s", webhookReq.TicketID)))
}

// ProcessWebhook processes a webhook request
func (j *JiraRetrievalAgent) ProcessWebhook(ctx context.Context, webhookReq *WebhookRequest) error {
	log.Printf("Processing webhook for ticket: %s, event: %s", webhookReq.TicketID, webhookReq.Event)

	// In a real implementation, fetch the ticket from Jira
	// Here we'll simulate the ticket for demo purposes
	ticket := &models.JiraTicket{
		Key:     webhookReq.TicketID,
		Summary: fmt.Sprintf("Sample ticket %s", webhookReq.TicketID),
		// Add other ticket details
	}

	// Create a TicketAvailableTask
	taskData := models.TicketAvailableTask{
		TicketID: ticket.Key,
		Summary:  ticket.Summary,
		Metadata: map[string]string{
			"event": webhookReq.Event,
			// Add other metadata as needed
		},
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
	log.Printf("Sending 'ticket-available' task to InfoGatheringAgent")
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