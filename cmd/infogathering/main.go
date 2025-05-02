package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tuannvm/jira-a2a/internal/agents"
	"github.com/tuannvm/jira-a2a/internal/config"
)

// Simulated A2A server and client interfaces
// In a real implementation, these would be provided by the tRPC-A2A-Go framework

// A2AServer represents an A2A server
type A2AServer struct {
	agent  agents.TaskProcessor
	config *config.Config
}

// AgentCard represents the metadata for an agent
type AgentCard struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// A2AClient represents an A2A client
type A2AClient struct {
	serverURL string
}

// TaskHandleImpl implements the TaskHandle interface
type TaskHandleImpl struct {
	taskID string
}

// UpdateStatus updates the status of a task
func (h *TaskHandleImpl) UpdateStatus(status string) error {
	log.Printf("Task %s: Status updated to %s", h.taskID, status)
	return nil
}

// RecordArtifact records an artifact for a task
func (h *TaskHandleImpl) RecordArtifact(name, url string) error {
	log.Printf("Task %s: Artifact recorded - %s: %s", h.taskID, name, url)
	return nil
}

// Complete completes a task with a result
func (h *TaskHandleImpl) Complete(result []byte) error {
	log.Printf("Task %s: Completed with result: %s", h.taskID, string(result))
	return nil
}

// NewA2AServer creates a new A2A server
func NewA2AServer(agent agents.TaskProcessor, cfg *config.Config) *A2AServer {
	return &A2AServer{
		agent:  agent,
		config: cfg,
	}
}

// Start starts the A2A server
func (s *A2AServer) Start() error {
	// Create a new HTTP server
	addr := fmt.Sprintf("%s:%d", s.config.ServerHost, s.config.ServerPort)
	
	// Define the agent card
	agentCard := AgentCard{
		Name:         s.config.AgentName,
		URL:          s.config.AgentURL,
		Version:      s.config.AgentVersion,
		Capabilities: []string{"process-ticket-available"},
	}
	
	// Create a new HTTP server mux
	mux := http.NewServeMux()
	
	// Add a health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	
	// Add an agent card endpoint
	mux.HandleFunc("/agent-card", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agentCard)
	})
	
	// Add a task endpoint
	mux.HandleFunc("/task", s.handleTask)
	
	// Create a new HTTP server
	server := &http.Server{
		Addr:    addr,
		Handler: s.authMiddleware(mux),
	}
	
	// Start the server
	log.Printf("Starting A2A server on %s", addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()
	
	// Wait for a signal to shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	
	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	log.Println("Shutting down server...")
	return server.Shutdown(ctx)
}

// handleTask handles a task request
func (s *A2AServer) handleTask(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// Parse the request body
	var taskData json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&taskData); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	// Create a task handle
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	handle := &TaskHandleImpl{taskID: taskID}
	
	// Process the task in a goroutine
	go func() {
		if err := s.agent.Process(context.Background(), taskData, handle); err != nil {
			log.Printf("Failed to process task %s: %v", taskID, err)
		}
	}()
	
	// Return a success response
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(fmt.Sprintf(`{"taskId": "%s"}`, taskID)))
}

// authMiddleware adds authentication to the server
func (s *A2AServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip authentication for health check
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		
		// Check authentication
		var authValid bool
		
		switch s.config.AuthType {
		case "jwt":
			// JWT authentication would be implemented here
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				// Validate JWT token
				// This is a simplified example
				authValid = true
			}
		case "apikey":
			// API key authentication
			apiKey := r.Header.Get("X-API-Key")
			authValid = apiKey == s.config.APIKey
		default:
			authValid = false
		}
		
		if !authValid {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// NewA2AClient creates a new A2A client
func NewA2AClient(serverURL string) *A2AClient {
	return &A2AClient{
		serverURL: serverURL,
	}
}

// SendTask sends a task to an A2A server
func (c *A2AClient) SendTask(taskType string, taskData interface{}, apiKey string) error {
	// Marshal the task data
	jsonData, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("failed to marshal task data: %w", err)
	}
	
	// Create a new HTTP request
	req, err := http.NewRequest("POST", c.serverURL+"/task", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	
	// Send the request
	client := &http.Client{Timeout: time.Second * 10}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	// Check the response
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send task: status %d, body: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

func main() {
	// Check command line arguments
	if len(os.Args) > 1 && os.Args[1] == "client" {
		// Run the client example
		clientExample()
		return
	} else if len(os.Args) > 1 && os.Args[1] == "webhook" {
		// Show the webhook simulation example
		simulateJiraWebhook()
		return
	}

	// Create a new configuration
	cfg := config.NewConfig()
	
	// Create a new InformationGatheringAgent
	agent := agents.NewInformationGatheringAgent(cfg)
	
	// Create a new A2A server
	server := NewA2AServer(agent, cfg)
	
	// Print usage information
	fmt.Println("Starting InformationGatheringAgent server...")
	fmt.Printf("Server will listen on %s:%d\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Println("To run the client example, use: ./infogathering client")
	fmt.Println("To see webhook simulation, use: ./infogathering webhook")
	
	// Start the server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
