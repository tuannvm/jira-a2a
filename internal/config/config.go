package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Default port constants
const (
	DefaultJiraRetrievalPort = "8080"
	DefaultInfoGatheringPort = "8081"
	DefaultCopilotPort      = "8082"
	DefaultUnknownPort      = "8080"
)

// Agent name constants
const (
	JiraRetrievalAgentName    = "JiraRetrievalAgent"
	InfoGatheringAgentName    = "InformationGatheringAgent"
	CopilotAgentName          = "CopilotAgent"
)

// Config holds the application configuration
type Config struct {
	// Server configuration
	ServerPort int
	ServerHost string

	// Agent configuration
	AgentName    string
	AgentVersion string
	AgentURL     string

	// Jira configuration
	JiraBaseURL  string
	JiraUsername string
	JiraAPIToken string

	// Authentication
	AuthType  string // "jwt" or "apikey"
	JWTSecret string
	APIKey    string
	
	// LLM configuration
	LLMEnabled     bool
	LLMProvider    string // "openai", "azure", "anthropic"
	LLMModel       string
	LLMAPIKey      string
	LLMServiceURL  string
	LLMMaxTokens   int
	LLMTimeout     int // in seconds
	LLMTemperature float64
}

// init loads environment variables from .env file
func init() {
	// Try to load from project root first
	err := godotenv.Load()
	if err != nil {
		// Try loading from parent directory (assuming we're in a subdirectory)
		err = godotenv.Load("../.env")
		if err != nil {
			// Try one more level up
			err = godotenv.Load("../../.env")
			if err != nil {
				log.Println("No .env file found or error loading it. Using environment variables or defaults.")
			} else {
				log.Println("Loaded configuration from ../../.env file")
			}
		} else {
			log.Println("Loaded configuration from ../.env file")
		}
	} else {
		log.Println("Loaded configuration from .env file")
	}
}

// NewConfig creates a new configuration with values from environment variables
func NewConfig() *Config {
	// Get the agent name first
	agentName := getEnvOrDefault("AGENT_NAME", "InformationGatheringAgent")
	
	// Set default port based on agent name
	var defaultPort string
	switch agentName {
	case JiraRetrievalAgentName:
		defaultPort = DefaultJiraRetrievalPort
	case InfoGatheringAgentName:
		defaultPort = DefaultInfoGatheringPort
	case CopilotAgentName:
		defaultPort = DefaultCopilotPort
	default:
		// Default for unknown agents
		defaultPort = DefaultUnknownPort
	}
	
	// Log the agent name and default port for debugging
	log.Printf("Agent: %s, Default port: %s", agentName, defaultPort)
	
	port, _ := strconv.Atoi(getEnvOrDefault("SERVER_PORT", defaultPort))
	
	llmMaxTokens, _ := strconv.Atoi(getEnvOrDefault("LLM_MAX_TOKENS", "4000"))
	llmTimeout, _ := strconv.Atoi(getEnvOrDefault("LLM_TIMEOUT", "30"))
	llmEnabled, _ := strconv.ParseBool(getEnvOrDefault("LLM_ENABLED", "false"))
	llmTemperature, _ := strconv.ParseFloat(getEnvOrDefault("LLM_TEMPERATURE", "0.0"), 64)

	// Create default agent URL with the correct port
	defaultAgentURL := fmt.Sprintf("http://localhost:%d", port)
	
	return &Config{
		// Server configuration
		ServerPort: port,
		ServerHost: getEnvOrDefault("SERVER_HOST", "localhost"),

		// Agent configuration
		AgentName:    agentName,
		AgentVersion: getEnvOrDefault("AGENT_VERSION", "1.0.0"),
		AgentURL:     getEnvOrDefault("AGENT_URL", defaultAgentURL),

		// Jira configuration
		JiraBaseURL:  getEnvOrDefault("JIRA_BASE_URL", "https://your-jira-instance.atlassian.net"),
		JiraUsername: getEnvOrDefault("JIRA_USERNAME", ""),
		JiraAPIToken: getEnvOrDefault("JIRA_API_TOKEN", ""),

		// Authentication
		AuthType:  getEnvOrDefault("AUTH_TYPE", "apikey"), // "jwt" or "apikey"
		JWTSecret: getEnvOrDefault("JWT_SECRET", "your-jwt-secret"),
		APIKey:    getEnvOrDefault("API_KEY", "your-api-key"),
		
		// LLM configuration
		LLMEnabled:     llmEnabled,
		LLMProvider:    getEnvOrDefault("LLM_PROVIDER", "openai"),
		LLMModel:       getEnvOrDefault("LLM_MODEL", "gpt-4"),
		LLMAPIKey:      getEnvOrDefault("LLM_API_KEY", ""),
		LLMServiceURL:  getEnvOrDefault("LLM_SERVICE_URL", ""),
		LLMMaxTokens:   llmMaxTokens,
		LLMTimeout:     llmTimeout,
		LLMTemperature: llmTemperature,
	}
}

// getEnvOrDefault returns the value of the environment variable or a default value
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
