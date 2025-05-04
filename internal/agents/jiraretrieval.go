package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/common"
	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/jira"
	"github.com/tuannvm/jira-a2a/internal/models"
	a2aclient "trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type JiraRetrievalAgent struct {
	cfg             *config.Config
	jiraClient      *jira.Client
	infoAgentClient *a2aclient.A2AClient
	a2aServer       *server.A2AServer
	httpMux         *http.ServeMux
}

// NewJiraRetrievalAgent creates a new agent for handling Jira webhooks and A2A tasks.
func NewJiraRetrievalAgent(cfg *config.Config) *JiraRetrievalAgent {
	jiraCli := jira.NewClient(cfg)
	// InformationGatheringAgent is on next port
	infoURL := fmt.Sprintf("http://%s:%d", cfg.ServerHost, cfg.ServerPort+1)
	a2aClient, err := common.SetupA2AClient(cfg, infoURL)
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}
	mux := http.NewServeMux()
	return &JiraRetrievalAgent{
		cfg:             cfg,
		jiraClient:      jiraCli,
		infoAgentClient: a2aClient,
		httpMux:         mux,
	}
}

// SetupA2AServer configures the A2A server to receive analysis responses.
func (j *JiraRetrievalAgent) SetupA2AServer() error {
	skills := []server.AgentSkill{{
		ID:          "process-info-gathered",
		Name:        "Process Info Gathered",
		Description: common.StringPtr("Processes info from InformationGatheringAgent"),
		Tags:        []string{"ticket", "comment"},
		InputModes:  []string{"text", "data"},
		OutputModes: []string{"text", "data"},
	}}
	opts := common.SetupServerOptions{
		AgentName:    j.cfg.AgentName,
		AgentVersion: j.cfg.AgentVersion,
		AgentURL:     j.cfg.AgentURL,
		AuthType:     j.cfg.AuthType,
		JWTSecret:    j.cfg.JWTSecret,
		APIKey:       j.cfg.APIKey,
		Processor:    j,
		Skills:       skills,
	}
	srv, err := common.SetupServer(opts)
	if err != nil {
		return fmt.Errorf("failed to setup A2A server: %w", err)
	}
	j.a2aServer = srv
	return nil
}

// StartA2AServer starts the A2A server for processing incoming tasks.
func (j *JiraRetrievalAgent) StartA2AServer(ctx context.Context) error {
	return common.StartServer(ctx, j.a2aServer, j.cfg.ServerHost, j.cfg.ServerPort)
}

// SetupHTTPServer registers the webhook handler.
func (j *JiraRetrievalAgent) SetupHTTPServer() {
	j.httpMux.HandleFunc("/webhook", j.handleWebhook)
}

// StartHTTPServer starts an HTTP server for Jira webhook events.
func (j *JiraRetrievalAgent) StartHTTPServer() error {
	addr := fmt.Sprintf("%s:%d", j.cfg.ServerHost, j.cfg.WebhookPort)
	log.Infof("Starting webhook server on %s", addr)
	return http.ListenAndServe(addr, j.httpMux)
}

// handleWebhook processes Jira webhook requests.
func (j *JiraRetrievalAgent) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	webReq, err := jira.TransformJiraWebhook(body)
	if err != nil {
		http.Error(w, "Invalid webhook payload", http.StatusBadRequest)
		return
	}
	if err := j.ProcessWebhook(r.Context(), webReq); err != nil {
		http.Error(w, fmt.Sprintf("Failed to process webhook: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Webhook processed for ticket %s", webReq.TicketID)))
}

// ProcessWebhook fetches ticket data and forwards it to InformationGatheringAgent.
func (j *JiraRetrievalAgent) ProcessWebhook(ctx context.Context, webReq *jira.WebhookRequest) error {
	log.Infof("Processing Jira webhook for ticket %s, event %s", webReq.TicketID, webReq.Event)
	ticket, err := j.jiraClient.GetTicket(webReq.TicketID)
	if err != nil {
		log.Errorf("Jira API fetch failed for ticket %s: %v", webReq.TicketID, err)
		// Use client ticket type for fallback
		ticket = &jira.ClientJiraTicket{Key: webReq.TicketID, Summary: webReq.TicketID}
	}
	// Convert Changes map to JSON string
	rawChanges, _ := json.Marshal(webReq.Changes)
	taskData := models.TicketAvailableTask{
		TicketID:    ticket.Key,
		Summary:     ticket.Summary,
		Description: ticket.Description,
		Status:      fmt.Sprintf("%v", ticket.Fields["status"]),
		Reporter:    fmt.Sprintf("%v", ticket.Fields["reporter"]),
		Assignee:    fmt.Sprintf("%v", ticket.Fields["assignee"]),
		Priority:    fmt.Sprintf("%v", ticket.Fields["priority"]),
		Labels:      toStringSlice(ticket.Fields["labels"]),
		Created:     fmt.Sprintf("%v", ticket.Fields["created"]),
		Updated:     fmt.Sprintf("%v", ticket.Fields["updated"]),
		Changes:     string(rawChanges),
		Metadata:    webReq.CustomFields,
	}
	// Send task data as DataPart with explicit type and metadata
	msg := protocol.Message{Parts: []protocol.Part{&protocol.DataPart{
		Type:     "data",
		Data:     taskData,
		Metadata: map[string]interface{}{"content-type": "application/json"},
	}}}
	log.Infof("Sending TicketAvailableTask for ticket %s to InformationGatheringAgent", ticket.Key)
	resp, err := j.infoAgentClient.SendTasks(ctx, protocol.SendTaskParams{Message: msg})
	if err != nil {
		log.Errorf("Failed to send TicketAvailableTask for ticket %s: %v", ticket.Key, err)
		return fmt.Errorf("failed to send task: %w", err)
	}
	log.Infof("Successfully sent TicketAvailableTask to InformationGatheringAgent (Task ID: %s)", resp.ID)
	return nil
}

// Process handles responses (InfoGatheredTask) from InformationGatheringAgent.
func (j *JiraRetrievalAgent) Process(ctx context.Context, taskID string, msg protocol.Message, handle taskmanager.TaskHandle) error {
	var infoTask models.InfoGatheredTask
	log.Infof("JiraRetrievalAgent received InfoGatheredTask (Task ID: %s)", taskID)

	if err := common.ExtractInfoGatheredTask(&msg, &infoTask); err != nil {
		log.Errorf("Failed to extract InfoGatheredTask for Task ID %s: %v", taskID, err)
		return fmt.Errorf("invalid InfoGatheredTask: %w", err)
	}
	log.Infof("Processing InfoGatheredTask for ticket %s", infoTask.TicketID)

	// Format and post comment to Jira
	commentText := j.formatJiraComment(&infoTask)
	log.Infof("Posting comment to Jira API for ticket %s", infoTask.TicketID)
	cmt, err := j.jiraClient.PostComment(infoTask.TicketID, commentText)
	if err != nil {
		log.Errorf("Failed to post comment for ticket %s: %v", infoTask.TicketID, err)
	} else {
		log.Infof("Successfully posted comment to Jira API for ticket %s (URL: %s)", infoTask.TicketID, cmt.URL)
	}

	response := protocol.NewTextPart(fmt.Sprintf("Comment posted for ticket %s", infoTask.TicketID))
	if err := handle.UpdateStatus(protocol.TaskState("completed"), &protocol.Message{Parts: []protocol.Part{response}}); err != nil {
		return err
	}
	return nil
}

func toStringSlice(val interface{}) []string {
	if arr, ok := val.([]interface{}); ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			out = append(out, fmt.Sprintf("%v", v))
		}
		return out
	}
	return nil
}

// formatJiraComment creates a Jira comment from gathered info.
func (j *JiraRetrievalAgent) formatJiraComment(task *models.InfoGatheredTask) string {
	var sb strings.Builder
	sb.WriteString("*Information Gathering Results*\n\n")
	sb.WriteString(fmt.Sprintf("*Summary:* %s\n\n", task.Summary))
	sb.WriteString("*Analysis:*\n")
	for k, v := range task.AnalysisResult {
		sb.WriteString(fmt.Sprintf("- *%s:* %s\n", common.Capitalize(k), v))
	}
	sb.WriteString("\n*LLM Summary:*\n")
	sb.WriteString(task.Summary)
	return sb.String()
}
