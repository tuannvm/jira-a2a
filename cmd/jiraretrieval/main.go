package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	// Create a new configuration
	cfg := config.NewConfig()
	
	// Override agent name for JiraRetrievalAgent
	cfg.AgentName = config.JiraRetrievalAgentName
	
	// Update agent URL to match the port
	cfg.AgentURL = fmt.Sprintf("http://%s:%d", cfg.ServerHost, cfg.ServerPort)
	
	// Log the configuration
	log.Printf("JiraRetrievalAgent configured with port: %d", cfg.ServerPort)

	// Create a new JiraRetrievalAgent
	agent := agents.NewJiraRetrievalAgent(cfg)

	// Print usage information
	fmt.Println("Starting JiraRetrievalAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Printf("Webhook endpoint: http://%s:%d/webhook\n", cfg.ServerHost, cfg.ServerPort + 3)
	fmt.Println("To run the client example, use: make test-client")

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the server and handle shutdown
	if err := agent.StartServer(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server shutdown complete")
}