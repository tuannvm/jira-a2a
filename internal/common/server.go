package common

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/auth"
	"trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// AgentInterface defines the common interface that all agents must implement
type AgentInterface interface {
	taskmanager.TaskProcessor
}

// SetupServerOptions contains options for setting up an A2A server
type SetupServerOptions struct {
	AgentName    string
	AgentVersion string
	AgentURL     string
	AuthType     string
	JWTSecret    string
	APIKey       string
	Processor    taskmanager.TaskProcessor
	Skills       []server.AgentSkill
}

// SetupServer creates and configures an A2A server with common settings
func SetupServer(opts SetupServerOptions) (*server.A2AServer, error) {
	// Define the agent card
	agentCard := server.AgentCard{
		Name:        opts.AgentName,
		Description: StringPtr(fmt.Sprintf("%s agent", opts.AgentName)),
		URL:         opts.AgentURL,
		Version:     opts.AgentVersion,
		Provider: &server.AgentProvider{
			Organization: "Your Organization",
		},
		DefaultInputModes:  []string{"text", "data"},
		DefaultOutputModes: []string{"text", "data"},
		Skills:             opts.Skills,
	}

	// Create task manager, inject processor
	taskManager, err := taskmanager.NewMemoryTaskManager(opts.Processor)
	if err != nil {
		return nil, fmt.Errorf("failed to create task manager: %w", err)
	}

	// Setup server options
	serverOpts := []server.Option{}
	// Enable JSON-RPC at root so A2AClient.SendTasks will POST to "/"
	serverOpts = append(serverOpts, server.WithJSONRPCEndpoint("/"))
	// Increase read/write timeouts for long-running JSON-RPC tasks
	serverOpts = append(serverOpts,
		server.WithReadTimeout(2*time.Minute),
		server.WithWriteTimeout(2*time.Minute),
	)

	// Add authentication if configured
	if opts.AuthType != "" {
		var authProvider auth.Provider
		switch opts.AuthType {
		case "jwt":
			log.Default.Infof("Configuring JWT authentication for %s", opts.AgentName)
			authProvider = auth.NewJWTAuthProvider(
				[]byte(opts.JWTSecret),
				"", // audience (empty for any)
				"", // issuer (empty for any)
				24*time.Hour,
			)
		case "apikey":
			log.Default.Infof("Configuring API key authentication for %s (API key length: %d)", opts.AgentName, len(opts.APIKey))
			apiKeys := map[string]string{
				opts.APIKey: "user",
			}
			authProvider = auth.NewAPIKeyAuthProvider(apiKeys, "X-API-Key")
		default:
			log.Default.Warnf("Unsupported authentication type '%s', skipping auth setup", opts.AuthType)
			return nil, fmt.Errorf("unsupported auth type: %s", opts.AuthType)
		}
		serverOpts = append(serverOpts, server.WithAuthProvider(authProvider))
	} else {
		log.Default.Warnf("No authentication configured for %s, running unauthenticated", opts.AgentName)
	}

	// Create the server
	srv, err := server.NewA2AServer(agentCard, taskManager, serverOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	return srv, nil
}

// StartServer starts the A2A server and handles graceful shutdown
func StartServer(ctx context.Context, srv *server.A2AServer, host string, port int) error {
	// Start the server in a goroutine
	addr := fmt.Sprintf("%s:%d", host, port)
	go func() {
		log.Default.Infof("Starting A2A server on %s", addr)
		if err := srv.Start(addr); err != nil {
			log.Default.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Create a context with a timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server
	log.Default.Infof("Shutting down server...")
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}

// AuthMiddleware creates an HTTP middleware for authentication
func AuthMiddleware(provider auth.Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract authentication information
		authInfo, err := provider.Authenticate(r)
		if err != nil {
			ReturnJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		// Store authentication info in the request context
		ctx := context.WithValue(r.Context(), "auth_info", authInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
