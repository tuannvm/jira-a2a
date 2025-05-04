package common

import (
	"context"
	"fmt"
	"log"

	"github.com/tuannvm/jira-a2a/internal/config"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

// SetupA2AClient creates and configures an A2A client with appropriate authentication
func SetupA2AClient(cfg *config.Config, targetURL string) (*client.A2AClient, error) {
	var a2aClient *client.A2AClient
	var err error

	// Create client with appropriate authentication
	switch cfg.AuthType {
	case "jwt":
		// JWT authentication
		log.Printf("Using JWT authentication for A2A client")
		a2aClient, err = client.NewA2AClient(targetURL)
	case "apikey":
		// API key authentication
		log.Printf("Using API key authentication for A2A client (API key length: %d)", len(cfg.APIKey))
		a2aClient, err = client.NewA2AClient(targetURL, client.WithAPIKeyAuth(cfg.APIKey, "X-API-Key"))
	default:
		// Default to no authentication
		log.Printf("Warning: No authentication configured for A2A client")
		a2aClient, err = client.NewA2AClient(targetURL)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create A2A client: %w", err)
	}

	return a2aClient, nil
}

// SendTask synchronously sends a task via JSON-RPC and returns the consolidated Message.
func SendTask(ctx context.Context, a2aClient *client.A2AClient, params protocol.SendTaskParams) (protocol.Message, error) {
	task, err := a2aClient.SendTasks(ctx, params)
	if err != nil {
		return protocol.Message{}, fmt.Errorf("SendTasks RPC failed: %w", err)
	}
	var parts []protocol.Part
	for _, art := range task.Artifacts {
		parts = append(parts, art.Parts...)
	}
	return protocol.Message{Parts: parts}, nil
}
