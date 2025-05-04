package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/common"
	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/llm"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// InformationGatheringAgent analyzes Jira ticket information received from JiraRetrievalAgent.
// It uses LLM (if configured) and returns structured insights.
// It does not interact directly with the Jira API.
type InformationGatheringAgent struct {
	config    *config.Config
	llmClient llm.LLMClient
	server    *server.A2AServer
}

// NewInformationGatheringAgent creates a new InformationGatheringAgent.
func NewInformationGatheringAgent(cfg *config.Config) *InformationGatheringAgent {
	var llmClient llm.LLMClient

	if cfg.LLMEnabled {
		var err error
		llmClient, err = llm.NewClient(cfg)
		if err != nil {
			log.Warnf("Failed to initialize LLM client: %v. LLM features disabled.", err)
			llmClient = nil // Ensure LLM is disabled if init fails
		}
	}

	return &InformationGatheringAgent{
		config:    cfg,
		llmClient: llmClient,
	}
}

// SetupAgentServer configures the A2A server for the agent.
func (a *InformationGatheringAgent) SetupAgentServer() error {
	skills := []server.AgentSkill{
		{
			ID:          "process-ticket-info",
			Name:        "Process Ticket Information",
			Description: common.StringPtr("Analyzes ticket information and provides insights"),
			Tags:        []string{"analysis", "ticket"},
			InputModes:  []string{"text", "data"},
			OutputModes: []string{"text", "data"},
		},
	}

	opts := common.SetupServerOptions{
		AgentName:    a.config.AgentName,
		AgentVersion: a.config.AgentVersion,
		AgentURL:     a.config.AgentURL,
		AuthType:     a.config.AuthType,
		JWTSecret:    a.config.JWTSecret,
		APIKey:       a.config.APIKey,
		Processor:    a, // The agent itself implements the processor interface
		Skills:       skills,
	}

	srv, err := common.SetupServer(opts)
	if err != nil {
		return fmt.Errorf("failed to setup server for InformationGatheringAgent: %w", err)
	}
	a.server = srv
	return nil
}

// StartAgentServer starts the configured A2A server.
func (a *InformationGatheringAgent) StartAgentServer(ctx context.Context) error {
	if a.server == nil {
		return fmt.Errorf("server not setup for InformationGatheringAgent")
	}
	return common.StartServer(ctx, a.server, a.config.ServerHost, a.config.ServerPort)
}

// Process implements the taskmanager.TaskProcessor interface.
// It receives TicketAvailableTask, analyzes it, and returns InfoGatheredTask.
func (a *InformationGatheringAgent) Process(ctx context.Context, taskID string, message protocol.Message, handle taskmanager.TaskHandle) error {
	log.Infof("InformationGatheringAgent received task %s", taskID)

	// 1. Extract TicketAvailableTask from the message
	var ticketTask models.TicketAvailableTask
	// Use the specific function for extracting TicketAvailableTask
	if err := common.ExtractTicketData(message, &ticketTask); err != nil {
		errMsg := fmt.Sprintf("failed to extract TicketAvailableTask data for task %s: %v", taskID, err)
		log.Errorf(errMsg)
		return fmt.Errorf(errMsg)
	}

	log.Infof("Processing TicketAvailableTask for ticket %s (Task ID: %s)", ticketTask.TicketID, taskID)

	// 2. Analyze the ticket information (using LLM if available)
	analysisResult, err := a.analyzeTicketInfo(&ticketTask)
	if err != nil {
		errMsg := fmt.Sprintf("failed to analyze ticket info for task %s: %v", taskID, err)
		log.Errorf(errMsg)
		return fmt.Errorf(errMsg)
	}

	// 3. Generate a summary (using LLM if available)
	var summary string
	if a.llmClient != nil {
		summary, err = a.generateSummary(&ticketTask, analysisResult)
		if err != nil {
			log.Warnf("Failed to generate LLM summary for task %s: %v", taskID, err)
			summary = "Summary generation failed: " + err.Error()
		} else {
			log.Infof("Generated LLM summary for task %s", taskID)
		}
	} else {
		summary = "LLM is disabled. No summary generated."
		log.Infof("LLM disabled, skipping summary generation for task %s", taskID)
	}

	// 4. Create InfoGatheredTask with results
	infoGatheredTask := models.InfoGatheredTask{
		TaskID:         taskID,
		TicketID:       ticketTask.TicketID,
		AnalysisResult: analysisResult,
		Summary:        summary,
	}

	// 5. Create the result message
	resultMessage := protocol.Message{
		Parts: []protocol.Part{
			&protocol.DataPart{
				Type: "data",
				Data: infoGatheredTask,
				Metadata: map[string]interface{}{
					"content-type": "application/json",
				},
			},
		},
	}

	// 6. Update task status to completed with the result message
	log.Infof("Completing task %s with InfoGatheredTask for ticket %s", taskID, ticketTask.TicketID)
	if err := handle.UpdateStatus(protocol.TaskStateCompleted, &protocol.Message{
		Parts: []protocol.Part{resultMessage.Parts[0]},
	}); err != nil {
		errMsg := fmt.Sprintf("failed to update status for task %s: %v", taskID, err)
		log.Errorf(errMsg)
		return fmt.Errorf(errMsg)
	}
	// 7. Return nil to indicate successful processing
	return nil
}

// analyzeTicketInfo analyzes the ticket information using LLM (if available).
func (a *InformationGatheringAgent) analyzeTicketInfo(task *models.TicketAvailableTask) (map[string]string, error) {
	if a.llmClient == nil {
		log.Infof("LLM client not available, skipping analysis for ticket %s", task.TicketID)
		return map[string]string{"status": "LLM analysis skipped (client unavailable)"}, nil
	}

	log.Infof("Performing LLM analysis for ticket %s", task.TicketID)
	prompt := a.createLLMPrompt(task)
	response, err := a.llmClient.Complete(context.Background(), prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	return a.parseLLMResponse(response)
}

// createLLMPrompt creates a prompt for the LLM based on the ticket information.
func (a *InformationGatheringAgent) createLLMPrompt(task *models.TicketAvailableTask) string {
	// Using a raw string literal to avoid issues with special characters
	promptTemplate := `Analyze the following Jira ticket information and provide a structured analysis in JSON format.

Ticket ID: %s
Summary: %s
Status: %s
Reporter: %s
Assignee: %s
Priority: %s
Labels: %s
Created: %s
Updated: %s

Description:
%s

Recent Changes:
%s

Please provide a JSON object containing the following fields:
- Sentiment: (Positive/Negative/Neutral)
- Urgency: (Low/Medium/High/Critical)
- KeyInformation: Bullet points summarizing the core issue or request.
- DetectedEntities: Any important entities like product names, user IDs, error codes, etc.
- SuggestedAction: What should be the immediate next step?
- EstimatedEffort: (e.g., Small, Medium, Large, X-Large)
- RelatedTickets: Any mentioned related ticket IDs.
- RequiresClarification: (true/false) Does the description lack necessary information?
- RecommendedLabels: Suggested labels that should be added (as a JSON array of strings).

You may include additional fields that you think are relevant.
Ensure your analysis is concise but comprehensive.
Focus on extracting actionable insights.

JSON Analysis:
`

	return fmt.Sprintf(promptTemplate,
		task.TicketID,
		task.Summary,
		task.Status,
		task.Reporter,
		task.Assignee,
		task.Priority,
		strings.Join(task.Labels, ", "),
		task.Created,
		task.Updated,
		task.Description,
		task.Changes,
	)
}

// parseLLMResponse parses the LLM response (expected to contain JSON) into a map.
func (a *InformationGatheringAgent) parseLLMResponse(response string) (map[string]string, error) {
	jsonStr, err := common.ExtractJSON(response) // Use common utility
	if err != nil {
		log.Warnf("Failed to extract JSON from LLM response. Response was: %s", response)
		return nil, fmt.Errorf("failed to extract JSON from LLM response: %w. Raw response logged.", err)
	}

	var resultMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &resultMap); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from LLM response: %w", err)
	}

	// Convert map[string]interface{} to map[string]string for simplicity
	stringMap := make(map[string]string)
	for k, v := range resultMap {
		switch value := v.(type) {
		case string:
			stringMap[k] = value
		case []interface{}: // Handle array (e.g., RecommendedLabels)
			strArr := make([]string, 0, len(value))
			for _, item := range value {
				if str, ok := item.(string); ok {
					strArr = append(strArr, str)
				}
			}
			stringMap[k] = strings.Join(strArr, ", ")
		default:
			stringMap[k] = fmt.Sprintf("%v", value)
		}
	}

	return stringMap, nil
}

// generateSummary generates a human-readable summary using the LLM.
func (a *InformationGatheringAgent) generateSummary(task *models.TicketAvailableTask, analysis map[string]string) (string, error) {
	if a.llmClient == nil {
		return "LLM client not available for summary generation.", nil
	}

	log.Infof("Generating LLM summary for ticket %s", task.TicketID)
	// Convert analysis map back to a string format for the prompt
	analysisStr := ""
	for k, v := range analysis {
		analysisStr += fmt.Sprintf("- %s: %s\n", common.Capitalize(k), v)
	}

	promptTemplate := `Based on the following Jira ticket and its analysis, create a concise, human-readable summary suitable for a quick overview.

Ticket ID: %s
Summary: %s
Status: %s

Analysis Results:
%s
Please provide a brief summary (2-4 sentences) highlighting the main point and any critical findings or suggested actions.

Summary:
`

	prompt := fmt.Sprintf(promptTemplate,
		task.TicketID,
		task.Summary,
		task.Status,
		analysisStr,
	)

	response, err := a.llmClient.Complete(context.Background(), prompt)
	if err != nil {
		return "", fmt.Errorf("LLM summary completion failed: %w", err)
	}

	// Clean up the response (LLMs sometimes add extra text)
	summary := strings.TrimSpace(response)
	if strings.HasPrefix(summary, "Summary:") {
		summary = strings.TrimSpace(strings.TrimPrefix(summary, "Summary:"))
	}

	return summary, nil
}
