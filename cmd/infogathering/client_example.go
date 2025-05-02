package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/tuannvm/jira-a2a/pkg/models"
)

// Example of how to use the A2A client to send a "ticket-available" task
func clientExample() {
	// Create a new A2A client
	client := NewA2AClient("http://localhost:8080")

	// Create a new "ticket-available" task
	task := models.TicketAvailableTask{
		TicketID: "PROJ-123",
		Summary:  "Implement new feature",
		Metadata: map[string]string{
			"priority": "High",
			"reporter": "John Doe",
		},
	}

	// Send the task
	fmt.Println("Sending ticket-available task to the InformationGatheringAgent...")
	if err := client.SendTask("ticket-available", task, "your-api-key"); err != nil {
		log.Fatalf("Failed to send task: %v", err)
	}

	fmt.Println("Task sent successfully!")
}

// Example of how to simulate a Jira webhook
func simulateJiraWebhook() {
   // Create a sample "ticket-available" A2A task
   task := models.TicketAvailableTask{
       TicketID: "PROJ-123",
       Summary:  "Implement new feature",
       Metadata: map[string]string{
           "priority": "High",
           "reporter": "John Doe",
       },
   }
   // Marshal the task to JSON
   jsonPayload, err := json.MarshalIndent(task, "", "  ")
   if err != nil {
       log.Fatalf("Failed to marshal task payload: %v", err)
   }
   // Print the curl command to simulate sending the A2A task
   fmt.Println("To simulate a \"ticket-available\" A2A task, run the following curl command:")
   fmt.Printf(`
curl -X POST http://localhost:8080/task \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key" \
  -d '%s'
`, string(jsonPayload))
}
