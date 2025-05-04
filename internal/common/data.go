package common

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tuannvm/jira-a2a/internal/models"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

// ExtractTicketData extracts ticket data from a message
func ExtractTicketData(message protocol.Message, task *models.TicketAvailableTask) error {
	if len(message.Parts) == 0 {
		return fmt.Errorf("message has no parts")
	}

	// Try to extract from each part
	for _, part := range message.Parts {
		// Try DataPart first (value or pointer)
		var dp *protocol.DataPart
		switch v := part.(type) {
		case protocol.DataPart:
			dp = &v
		case *protocol.DataPart:
			dp = v
		}
		if dp != nil {
			log.Printf("Found DataPart")
			// Marshal Data field to raw JSON
			raw, err := json.Marshal(dp.Data)
			if err != nil {
				log.Printf("Failed to marshal DataPart.Data: %v", err)
				continue
			}
			// Try direct unmarshal into struct
			if err := json.Unmarshal(raw, task); err == nil && task.TicketID != "" && task.Summary != "" {
				log.Printf("Successfully extracted task data from DataPart")
				return nil
			}
			// Try as generic map
			var dataMap map[string]interface{}
			if err := json.Unmarshal(raw, &dataMap); err == nil {
				log.Printf("Parsed DataPart as map with %d keys", len(dataMap))
				if err := ExtractFromMap(dataMap, task); err == nil {
					return nil
				}
			}
		}
		
		// Try TextPart
		if textPart, ok := part.(*protocol.TextPart); ok && textPart != nil {
			log.Printf("Found TextPart with content: %s", textPart.Text)
			
			// Try to unmarshal the text as JSON
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				// Validate required fields
				if task.TicketID != "" && task.Summary != "" {
					log.Printf("Successfully extracted task data from TextPart")
					return nil
				}
			}
			
			// If direct unmarshal failed, try to parse as map
			var dataMap map[string]interface{}
			if err := json.Unmarshal([]byte(textPart.Text), &dataMap); err == nil {
				log.Printf("Parsed TextPart as map with %d keys", len(dataMap))
				
				// Extract data from map
				if err := ExtractFromMap(dataMap, task); err == nil {
					return nil
				}
			}
		}
	}

	return fmt.Errorf("could not extract ticket data from message")
}

// ExtractFromMap extracts ticket data from a map
func ExtractFromMap(data map[string]interface{}, task *models.TicketAvailableTask) error {
	// Log the keys for debugging
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	log.Printf("Map contains keys: %s", strings.Join(keys, ", "))
	
	// Extract ticket ID
	if ticketID, ok := GetStringValue(data, "ticketId", "ticket_id", "id"); ok {
		task.TicketID = ticketID
	} else {
		return fmt.Errorf("no ticket ID found in data")
	}
	
	// Extract summary
	if summary, ok := GetStringValue(data, "summary", "title", "name"); ok {
		task.Summary = summary
	} else {
		return fmt.Errorf("no summary found in data")
	}
	
	// Extract description (optional)
	if description, ok := GetStringValue(data, "description", "desc", "body"); ok {
		task.Description = description
	}
	
	// Extract metadata (optional)
	if task.Metadata == nil {
		task.Metadata = make(map[string]string)
	}
	
	// Add common metadata fields if present
	for _, field := range []string{"priority", "status", "assignee", "reporter", "type", "components"} {
		if value, ok := GetStringValue(data, field); ok {
			task.Metadata[field] = value
		}
	}
	
	// Check if we have the minimum required fields
	if task.TicketID != "" && task.Summary != "" {
		return nil
	}
	
	return fmt.Errorf("missing required fields in data")
}

// ExtractInfoGatheredTask extracts an InfoGatheredTask from a message
func ExtractInfoGatheredTask(message *protocol.Message, task *models.InfoGatheredTask) error {
	if message == nil || len(message.Parts) == 0 {
		return fmt.Errorf("message is nil or has no parts")
	}

	// Try to extract from each part
	for _, part := range message.Parts {
		// Check if it's a DataPart (pointer or value)
		var dp *protocol.DataPart
		switch v := part.(type) {
		case *protocol.DataPart:
			dp = v
		case protocol.DataPart:
			dp = &v
		}
		if dp != nil && dp.Data != nil {
			// Try to convert the data to InfoGatheredTask
			dataBytes, err := json.Marshal(dp.Data)
			if err == nil {
				if err := json.Unmarshal(dataBytes, task); err == nil {
					if task.TicketID != "" {
						return nil // Successfully extracted
					}
				}
			}
		}

		// Check if it's a TextPart
		textPart, ok := part.(*protocol.TextPart)
		if ok && textPart != nil && textPart.Text != "" {
			// Try to unmarshal the text as JSON
			if err := json.Unmarshal([]byte(textPart.Text), task); err == nil {
				if task.TicketID != "" {
					return nil // Successfully extracted
				}
			}
		}
	}

	return fmt.Errorf("could not extract InfoGatheredTask from message")
}
