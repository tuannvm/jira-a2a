package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/joho/godotenv"
	"github.com/tuannvm/jira-a2a/internal/config"
	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

// Test cases for various scenarios
func main() {
	// Load environment from various possible locations
	err := godotenv.Load()
	if err != nil {
		err = godotenv.Load("../.env")
		if err != nil {
			err = godotenv.Load("../../.env")
			if err != nil {
				log.Println("No .env file found, using environment variables")
			}
		}
	}

	// Create a new configuration
	cfg := config.NewConfig()

	// Create an A2A client
	a2aClient, err := client.NewA2AClient(cfg.AgentURL, client.WithAPIKeyAuth(cfg.APIKey, "X-API-Key"))
	if err != nil {
		log.Fatalf("Failed to create A2A client: %v", err)
	}

	// Define test cases
	testCases := []struct {
		name     string
		ticketID string
		summary  string
		metadata map[string]string
		expectOK bool
	}{
		{
			name:     "Valid ticket",
			ticketID: "PROJ-123",
			summary:  "Implement new feature",
			metadata: map[string]string{
				"priority": "High",
				"reporter": "John Doe",
			},
			expectOK: true,
		},
		{
			name:     "Missing summary",
			ticketID: "PROJ-124",
			summary:  "",
			metadata: nil,
			expectOK: false,
		},
		{
			name:     "Missing ticket ID",
			ticketID: "",
			summary:  "Test ticket",
			metadata: nil,
			expectOK: false,
		},
		// Add a real ticket from your Jira instance for end-to-end testing
		// {
		//     name:     "Real Jira ticket",
		//     ticketID: "REAL-123", // Replace with a real ticket ID from your instance
		//     summary:  "Real ticket summary",
		//     metadata: nil,
		//     expectOK: true,
		// },
	}

	// Run each test case
	for _, tc := range testCases {
		log.Printf("\nRunning test case: %s", tc.name)

		// Create the task data
		taskData := models.TicketAvailableTask{
			TicketID: tc.ticketID,
			Summary:  tc.summary,
			Metadata: tc.metadata,
		}

		// Marshal the task to JSON
		taskJSON, err := json.Marshal(taskData)
		if err != nil {
			log.Printf("Failed to marshal task: %v", err)
			continue
		}

		// Create a message with the task data
		textPart := protocol.NewTextPart(string(taskJSON))
		message := protocol.Message{
			Parts: []protocol.Part{textPart},
		}

		// Send the task
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		taskParams := protocol.SendTaskParams{
			Message: message,
		}

		log.Printf("Sending task to %s", cfg.AgentURL)
		resp, err := a2aClient.SendTasks(ctx, taskParams)
		if err != nil {
			if tc.expectOK {
				log.Printf("❌ Test failed: %v", err)
			} else {
				log.Printf("✅ Expected failure occurred: %v", err)
			}
			continue
		}

		log.Printf("Task sent successfully! Task ID: %s", resp.ID)

		// Poll for status updates
		log.Printf("Polling for task status...")
		for {
			time.Sleep(1 * time.Second)

			taskResult, err := a2aClient.GetTasks(ctx, protocol.TaskQueryParams{
				ID: resp.ID,
			})
			if err != nil {
				log.Printf("Failed to get task: %v", err)
				break
			}

			log.Printf("Task status: %s", taskResult.Status.State)

			if taskResult.Status.State == "completed" {
				log.Printf("✅ Task completed successfully!")

				// Extract and display result
				if taskResult.Status.Message != nil {
					for _, part := range taskResult.Status.Message.Parts {
						if textPart, ok := part.(*protocol.TextPart); ok {
							log.Printf("Result: %s", textPart.Text)

							// Try to parse as InfoGatheredTask
							var result models.InfoGatheredTask
							if err := json.Unmarshal([]byte(textPart.Text), &result); err == nil {
								log.Printf("Info gathered for ticket: %s", result.TicketID)
								log.Printf("Comment URL: %s", result.CommentURL)
								log.Printf("Collected fields: %+v", result.CollectedFields)
							}
						}
					}
				}

				// Display artifacts
				if len(taskResult.Artifacts) > 0 {
					log.Printf("Artifacts:")
					for i, artifact := range taskResult.Artifacts {
						name := ""
						if artifact.Name != nil {
							name = *artifact.Name
						}
						url := ""
						if artifact.Metadata != nil {
							if urlVal, ok := artifact.Metadata["url"]; ok {
								if urlStr, ok := urlVal.(string); ok {
									url = urlStr
								}
							}
						}
						log.Printf("%d. %s: %s", i+1, name, url)
					}
				}
				break
			} else if taskResult.Status.State == "failed" {
				if tc.expectOK {
					log.Printf("❌ Task failed unexpectedly")
				} else {
					log.Printf("✅ Task failed as expected")
				}
				break
			}

			// Timeout after 30 seconds
			if ctx.Err() != nil {
				log.Printf("❌ Test timed out")
				break
			}
		}
		log.Println("----------------------------------")
	}
}
