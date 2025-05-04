package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Agent name constants
const (
	JiraRetrievalAgentName    = "JiraRetrievalAgent"
	InfoGatheringAgentName    = "InformationGatheringAgent"
	CopilotAgentName          = "CopilotAgent"
)

// Default port values
const (
	DefaultJiraRetrievalPort = 8080
	DefaultInfoGatheringPort = 8081
	DefaultCopilotPort       = 8082
	DefaultWebhookPort       = 8083
)

// Config holds the application configuration
type Config struct {
	// Server configuration
	ServerPort int    `mapstructure:"server_port"`
	ServerHost string `mapstructure:"server_host"`

	// Agent configuration
	AgentName    string `mapstructure:"agent_name"`
	AgentVersion string `mapstructure:"agent_version"`
	AgentURL     string `mapstructure:"agent_url"`

	// Jira configuration
	JiraBaseURL  string `mapstructure:"jira_base_url"`
	JiraUsername string `mapstructure:"jira_username"`
	JiraAPIToken string `mapstructure:"jira_api_token"`

	// Authentication
	AuthType  string `mapstructure:"auth_type"`  // "jwt" or "apikey"
	JWTSecret string `mapstructure:"jwt_secret"`
	APIKey    string `mapstructure:"api_key"`
	
	// LLM configuration
	LLMEnabled     bool    `mapstructure:"llm_enabled"`
	LLMProvider    string  `mapstructure:"llm_provider"`  // "openai", "azure", "anthropic"
	LLMModel       string  `mapstructure:"llm_model"`
	LLMAPIKey      string  `mapstructure:"llm_api_key"`
	LLMServiceURL  string  `mapstructure:"llm_service_url"`
	LLMMaxTokens   int     `mapstructure:"llm_max_tokens"`
	LLMTimeout     int     `mapstructure:"llm_timeout"`      // in seconds
	LLMTemperature float64 `mapstructure:"llm_temperature"`
	
	// Webhook configuration
	WebhookPort int `mapstructure:"webhook_port"`
}

// viperInstance is the singleton instance of viper
var viperInstance *viper.Viper

// init initializes the viper configuration
func init() {
	// Initialize viper
	viperInstance = viper.New()
	
	// Set up viper to read environment variables
	viperInstance.AutomaticEnv()
	
	// Use underscores as separator in environment variables
	viperInstance.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	
	// Try to find and load the .env file from various possible locations
	possiblePaths := []string{
		".env",             // Current directory
		"../.env",          // Parent directory
		"../../.env",       // Two levels up
		"../../../.env",    // Three levels up
	}
	
	// Get the executable directory to try loading from there too
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		possiblePaths = append(possiblePaths, filepath.Join(execDir, ".env"))
	}
	
	// Try each path until we find a valid .env file
	loaded := false
	for _, path := range possiblePaths {
		viperInstance.SetConfigFile(path)
		err := viperInstance.ReadInConfig()
		if err == nil {
			log.Printf("Loaded configuration from %s file", path)
			loaded = true
			break
		}
	}
	
	if !loaded {
		log.Println("No .env file found or error loading it. Using environment variables or defaults.")
	}
	
	// Map standard environment variables to our configuration keys
	viperInstance.BindEnv("llm_api_key", "LLM_API_KEY", "OPENAI_API_KEY")
	// Log if we're using the OPENAI_API_KEY
	if os.Getenv("OPENAI_API_KEY") != "" && os.Getenv("LLM_API_KEY") == "" {
		log.Println("Using OPENAI_API_KEY environment variable for LLM API key")
	}
	
	// Set default values
	setDefaults()
}

// setDefaults sets default values for all configuration options
func setDefaults() {
	// Server configuration
	viperInstance.SetDefault("server_host", "localhost")
	// Don't set a default server_port here, it will be set based on agent name in NewConfig
	
	// Agent configuration
	viperInstance.SetDefault("agent_name", InfoGatheringAgentName)
	viperInstance.SetDefault("agent_version", "1.0.0")
	// AgentURL will be set dynamically in NewConfig
	
	// Jira configuration
	viperInstance.SetDefault("jira_base_url", "https://your-jira-instance.atlassian.net")
	viperInstance.SetDefault("jira_username", "")
	viperInstance.SetDefault("jira_api_token", "")
	
	// Authentication
	viperInstance.SetDefault("auth_type", "apikey") // "jwt" or "apikey"
	viperInstance.SetDefault("jwt_secret", "your-jwt-secret")
	viperInstance.SetDefault("api_key", "your-api-key")
	
	// LLM configuration
	viperInstance.SetDefault("llm_enabled", false)
	viperInstance.SetDefault("llm_provider", "openai")
	viperInstance.SetDefault("llm_model", "gpt-4")
	viperInstance.SetDefault("llm_api_key", "")
	viperInstance.SetDefault("llm_service_url", "")
	viperInstance.SetDefault("llm_max_tokens", 4000)
	viperInstance.SetDefault("llm_timeout", 30)
	viperInstance.SetDefault("llm_temperature", 0.0)
	
	// Webhook configuration
	viperInstance.SetDefault("webhook_port", DefaultWebhookPort)
}

// NewConfig creates a new configuration with values from environment variables and .env file
func NewConfig() *Config {
	// Get the agent name
	agentName := viperInstance.GetString("agent_name")
	
	// Set default port based on agent name
	var defaultPort int
	switch agentName {
	case JiraRetrievalAgentName:
		defaultPort = DefaultJiraRetrievalPort
	case InfoGatheringAgentName:
		defaultPort = DefaultInfoGatheringPort
	case CopilotAgentName:
		defaultPort = DefaultCopilotPort
	default:
		defaultPort = DefaultJiraRetrievalPort
	}
	
	// Override the server_port default if it hasn't been explicitly set
	if !viperInstance.IsSet("server_port") {
		viperInstance.Set("server_port", defaultPort)
	}
	
	// Log the agent name and port for debugging
	log.Printf("Configuring agent '%s' with default port: %d", agentName, defaultPort)
	
	// Get the server port and host
	port := viperInstance.GetInt("server_port")
	host := viperInstance.GetString("server_host")
	
	// Set the agent URL if not explicitly provided
	if !viperInstance.IsSet("agent_url") {
		viperInstance.Set("agent_url", fmt.Sprintf("http://%s:%d", host, port))
	}
	
	// Create the configuration
	config := &Config{}
	
	// Unmarshal the configuration from viper
	err := viperInstance.Unmarshal(config)
	if err != nil {
		log.Printf("Error unmarshaling configuration: %v", err)
	}
	
	// Log the configuration
	logConfig(config)
	
	return config
}

// logConfig logs the configuration values (excluding sensitive information)
func logConfig(config *Config) {
	log.Printf("Configuration loaded:")
	log.Printf("  Agent: %s", config.AgentName)
	log.Printf("  Server: %s:%d", config.ServerHost, config.ServerPort)
	log.Printf("  Webhook Port: %d", config.WebhookPort)
	log.Printf("  LLM Enabled: %v", config.LLMEnabled)
	
	// Log sensitive information as [REDACTED]
	if config.JiraUsername != "" {
		log.Printf("  Jira Username: [REDACTED]")
	}
	if config.JiraAPIToken != "" {
		log.Printf("  Jira API Token: [REDACTED]")
	}
	if config.JWTSecret != "" {
		log.Printf("  JWT Secret: [REDACTED]")
	}
	if config.APIKey != "" {
		log.Printf("  API Key: [REDACTED]")
	}
	if config.LLMAPIKey != "" {
		log.Printf("  LLM API Key: [REDACTED]")
	}
}

// GetViper returns the viper instance for direct access if needed
func GetViper() *viper.Viper {
	return viperInstance
}

// SetConfigForTesting sets a configuration value for testing purposes
func SetConfigForTesting(key string, value interface{}) {
	viperInstance.Set(key, value)
}
