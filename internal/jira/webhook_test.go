package jira

import (
	"os"
	"testing"
)

func TestTransformJiraWebhook(t *testing.T) {
	// Read the sample webhook JSON
	webhookData, err := os.ReadFile("../../docs/jira-webhook.json")
	if err != nil {
		t.Fatalf("Failed to read sample webhook JSON: %v", err)
	}

	// Transform the webhook
	webhookReq, err := TransformJiraWebhook(webhookData)
	if err != nil {
		t.Fatalf("Failed to transform webhook: %v", err)
	}

	// Check that the transformed webhook has the expected values
	expectedValues := map[string]string{
		"TicketID":  "JRA-20002",
		"Event":     "updated",
		"UserName":  "brollins",
		"UserEmail": "bryansemail at atlassian dot com",
		"ProjectKey": "JRA",
	}

	if webhookReq.TicketID != expectedValues["TicketID"] {
		t.Errorf("Expected TicketID to be %s, got %s", expectedValues["TicketID"], webhookReq.TicketID)
	}

	if webhookReq.Event != expectedValues["Event"] {
		t.Errorf("Expected Event to be %s, got %s", expectedValues["Event"], webhookReq.Event)
	}

	if webhookReq.UserName != expectedValues["UserName"] {
		t.Errorf("Expected UserName to be %s, got %s", expectedValues["UserName"], webhookReq.UserName)
	}

	if webhookReq.UserEmail != expectedValues["UserEmail"] {
		t.Errorf("Expected UserEmail to be %s, got %s", expectedValues["UserEmail"], webhookReq.UserEmail)
	}

	if webhookReq.ProjectKey != expectedValues["ProjectKey"] {
		t.Errorf("Expected ProjectKey to be %s, got %s", expectedValues["ProjectKey"], webhookReq.ProjectKey)
	}

	// Check that we have changes 
	if len(webhookReq.Changes) != 2 {
		t.Errorf("Expected 2 changes, got %d", len(webhookReq.Changes))
	}

	// Check a specific change
	if val, ok := webhookReq.Changes["summary"]; ok {
		if val != "A new summary." {
			t.Errorf("Expected summary change to be 'A new summary.', got '%s'", val)
		}
	} else {
		t.Error("Expected changes to include 'summary', but it didn't")
	}

	// Check custom fields
	if len(webhookReq.CustomFields) == 0 {
		t.Error("Expected CustomFields to have values, but it was empty")
	}

	// Check a specific custom field
	if val, ok := webhookReq.CustomFields["summary"]; ok {
		if val != "I feel the need for speed" {
			t.Errorf("Expected summary to be 'I feel the need for speed', got '%s'", val)
		}
	} else {
		t.Error("Expected CustomFields to include 'summary', but it didn't")
	}

	t.Logf("Webhook successfully transformed: %+v", webhookReq)
}