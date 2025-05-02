# Jira A2A Workflow

This project implements a DevOps workflow using the tRPC-A2A-Go framework. It consists of independent Go agents that communicate via A2A messages, with each agent implementing the standard TaskProcessor interface.

## Current Implementation

The current implementation focuses on the InformationGatheringAgent:

1. **InformationGatheringAgent**
   - Consumes "ticket-available" tasks
   - Fetches the Jira ticket's full description, acceptance criteria, embedded links, linked tickets, and due date
   - Posts a comment back on the Jira ticket summarizing the gathered information and highlighting missing fields
   - Completes by emitting an "info-gathered" message

## Project Structure

```
jira-a2a/
├── cmd/
│   └── infogathering/
│       ├── main.go
│       └── client_example.go
├── internal/
│   ├── agents/
│   │   └── infogathering.go
│   ├── jira/
│   │   └── client.go
│   └── config/
│       └── config.go
├── pkg/
│   └── models/
│       └── models.go
├── go.mod
└── README.md
```

## Setup

### Prerequisites

- Go 1.16 or higher
- Access to a Jira instance

### Configuration

The application uses environment variables for configuration:

```
# Server configuration
export SERVER_PORT=8080
export SERVER_HOST=localhost

# Agent configuration
export AGENT_NAME=InformationGatheringAgent
export AGENT_VERSION=1.0.0
export AGENT_URL=http://localhost:8080

# Jira configuration
export JIRA_BASE_URL=https://your-jira-instance.atlassian.net
export JIRA_USERNAME=your-jira-username
export JIRA_API_TOKEN=your-jira-api-token

# Authentication
export AUTH_TYPE=apikey  # "jwt" or "apikey"
export API_KEY=your-api-key
export JWT_SECRET=your-jwt-secret  # Only needed if AUTH_TYPE=jwt
```

### Running the Application

1. Build and run the InformationGatheringAgent:

```bash
cd cmd/infogathering
go build
./infogathering
```

2. The agent will start on the configured port (default: 8080) and listen for "ticket-available" tasks.

## Testing

### Simulating a Jira Webhook

You can simulate a Jira webhook by sending a POST request to the agent:

```bash
curl -X POST http://localhost:8080/task \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key" \
  -d '{
  "ticketId": "PROJ-123",
  "summary": "Implement new feature",
  "metadata": {
    "priority": "High",
    "reporter": "John Doe"
  }
}'
```

### Using the Client Example

The `client_example.go` file contains examples of how to use the A2A client to send a "ticket-available" task and how to simulate a Jira webhook.

## Future Work

1. **JiraRetrievalAgent**
   - Listen for new Jira ticket webhooks
   - Emit a "ticket-available" A2A task with ticket information

## License

[MIT](LICENSE)
