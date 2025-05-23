package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	log "github.com/tuannvm/jira-a2a/internal/logging"

	"github.com/tuannvm/jira-a2a/internal/agents"

	"github.com/tuannvm/jira-a2a/internal/config"
)

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

	// Set agent name for configuration
	config.GetViper().Set("agent_name", config.JiraRetrievalAgentName)

	// Create a new configuration
	cfg := config.NewConfig()

	// Log the configuration
	log.Infof("JiraRetrievalAgent configured with port: %d", cfg.ServerPort)

	// Create a new JiraRetrievalAgent
	agent := agents.NewJiraRetrievalAgent(cfg)

	// Print usage information
	fmt.Println("Starting JiraRetrievalAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Printf("Webhook endpoint: http://%s:%d/webhook\n", cfg.ServerHost, cfg.WebhookPort)
	fmt.Println("To run the client example, use: make test-client")

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Setup A2A server for responses from InformationGatheringAgent
	if err := agent.SetupA2AServer(); err != nil {
		log.Fatalf("Failed to setup A2A server: %v", err)
	}
	// Setup HTTP server for Jira webhooks
	agent.SetupHTTPServer()

	// Start both servers concurrently
	go func() {
		if err := agent.StartA2AServer(ctx); err != nil {
			log.Fatalf("A2A server error: %v", err)
		}
	}()
	if err := agent.StartHTTPServer(); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}

	log.Infof("Server shutdown complete")
}
