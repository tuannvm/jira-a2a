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
	} else if len(os.Args) > 1 && os.Args[1] == "webhook" {
		// Show the webhook simulation example
		fmt.Println("Webhook simulation functionality not implemented in this file.")
		return
	} else if len(os.Args) > 1 && os.Args[1] == "debug" {
		// Run the debug client
		fmt.Println("Debug client functionality not implemented in this file.")
		return
	}

	// Set agent name for configuration
	config.GetViper().Set("agent_name", config.InfoGatheringAgentName)

	// Create a new configuration
	cfg := config.NewConfig()

	// Log the configuration
	log.Infof("InformationGatheringAgent configured with port: %d", cfg.ServerPort)

	// Create a new InformationGatheringAgent
	agent := agents.NewInformationGatheringAgent(cfg)

	// Print usage information
	fmt.Println("Starting InformationGatheringAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Println("To run the client example, use: make test-client")

	// Create a context that will be canceled on SIGINT or SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Setup server
	if err := agent.SetupAgentServer(); err != nil {
		log.Fatalf("Failed to setup agent server: %v", err)
	}

	// Start server
	log.Infof("Starting InformationGatheringAgent server...")
	if err := agent.StartAgentServer(ctx); err != nil {
		log.Fatalf("Failed to start agent server: %v", err)
	}

	log.Infof("InformationGatheringAgent server stopped gracefully")
}
