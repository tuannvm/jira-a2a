package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tuannvm/jira-a2a/internal/agents"
	"github.com/tuannvm/jira-a2a/internal/config"
	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

func main() {
	// Check command line arguments
	if len(os.Args) > 1 && os.Args[1] == "client" {
		// Run the client example
		fmt.Println("Client functionality not implemented in this file.")
		fmt.Println("Please use the test_a2a client instead.")
		return
	} else if len(os.Args) > 1 && os.Args[1] == "webhook-test" {
		// Show the webhook simulation example
		fmt.Println("For webhook testing, use: curl -X POST -H \"Content-Type: application/json\" -d '{\"ticketId\":\"PROJ-123\",\"event\":\"created\"}' http://localhost:8081/webhook")
		return
	}

	// Create a new configuration
	cfg := config.NewConfig()
	
	// Override agent name for JiraRetrievalAgent
	cfg.AgentName = config.JiraRetrievalAgentName
	
	// Ensure the agent name is set correctly
	// The port will be set based on the agent name in the config package
	
	// Update agent URL to match the port
	cfg.AgentURL = fmt.Sprintf("http://%s:%d", cfg.ServerHost, cfg.ServerPort)
	
	// Log the configuration
	log.Printf("JiraRetrievalAgent configured with port: %d", cfg.ServerPort)

	// Create a new JiraRetrievalAgent
	agent := agents.NewJiraRetrievalAgent(cfg)

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(agent)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}

	// Define the agent card
	agentCard := server.AgentCard{
		Name:        cfg.AgentName,
		Description: stringPtr("Agent that retrieves information from Jira and handles webhooks"),
		URL:         cfg.AgentURL,
		Version:     cfg.AgentVersion,
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
				Description: stringPtr("Processes Jira webhook events and emits 'ticket-available' tasks"),
				Tags:        []string{"jira", "webhook", "ticket"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
			{
				ID:          "process-info-gathered",
				Name:        "Process Info Gathered",
				Description: stringPtr("Processes information gathered by InfoGatheringAgent and posts to Jira"),
				Tags:        []string{"jira", "comment", "information"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
			},
		},
	}

	// Setup server options
	serverOpts := []server.Option{}

	// Add authentication if enabled
	if cfg.AuthType != "" {
		var authProvider auth.Provider
		switch cfg.AuthType {
		case "jwt":
			authProvider = auth.NewJWTAuthProvider(
				[]byte(cfg.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			apiKeys := map[string]string{
				cfg.APIKey: "user",
			}
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			log.Fatalf("Unsupported auth type: %s", cfg.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
	}

	// Create the server
	srv, err := server.NewA2AServer(agentCard, taskManager, serverOpts...)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Add webhook handler
	if err := agent.RegisterWebhookHandler(srv); err != nil {
		log.Fatalf("Failed to register webhook handler: %v", err)
	}

	// Print usage information
	fmt.Println("Starting JiraRetrievalAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Printf("Webhook endpoint: http://%s:%d/webhook\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Println("To run the client example, use: make test-client")

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the server in a goroutine
	addr := fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort)
	go func() {
		log.Printf("Starting A2A server on %s", addr)
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
	log.Println("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		log.Fatalf("Failed to shutdown server: %v", err)
	}
}