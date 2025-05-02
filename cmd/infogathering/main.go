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
	} else if len(os.Args) > 1 && os.Args[1] == "webhook" {
		// Show the webhook simulation example
		fmt.Println("Webhook simulation functionality not implemented in this file.")
		return
	} else if len(os.Args) > 1 && os.Args[1] == "debug" {
		// Run the debug client
		fmt.Println("Debug client functionality not implemented in this file.")
		return
	}

	// Create a new configuration
	cfg := config.NewConfig()

	// Create a new InformationGatheringAgent
	agent := agents.NewInformationGatheringAgent(cfg)

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(agent)
	if err != nil {
		log.Fatalf("Failed to create task manager: %v", err)
	}

	// Define the agent card
	agentCard := server.AgentCard{
		Name:        cfg.AgentName,
		Description: stringPtr("Agent that gathers information from Jira tickets"),
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
				ID:          "process-ticket-available",
				Name:        "Process Ticket Available",
				Description: stringPtr("Processes a new or updated Jira ticket, gathering information and posting a comment"),
				Tags:        []string{"jira", "ticket", "information-gathering"},
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

	// Print usage information
	fmt.Println("Starting InformationGatheringAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
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
