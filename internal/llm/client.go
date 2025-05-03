package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tuannvm/jira-a2a/internal/config"
)

// LLMClient defines the interface for interacting with LLM services
type LLMClient interface {
	// Complete sends a prompt to the LLM and returns the completion
	Complete(ctx context.Context, prompt string) (string, error)
}

// Client implements the LLMClient interface using langchain-go
type Client struct {
	llm       llms.LLM
	maxTokens int
	timeout   time.Duration
}

// NewClient creates a new LLM client based on the provided configuration
func NewClient(cfg *config.Config) (LLMClient, error) {
	var llmModel llms.LLM
	var err error

	// Select LLM provider based on configuration
	switch cfg.LLMProvider {
	case "openai":
		// Initialize OpenAI
		llmModel, err = openai.New(
			openai.WithToken(cfg.LLMAPIKey),
			openai.WithModel(cfg.LLMModel),
		)
	case "azure":
		// Initialize Azure OpenAI
		llmModel, err = openai.New(
			openai.WithToken(cfg.LLMAPIKey),
			openai.WithModel(cfg.LLMModel),
			openai.WithBaseURL(cfg.LLMServiceURL),
		)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.LLMProvider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	return &Client{
		llm:       llmModel,
		maxTokens: cfg.LLMMaxTokens,
		timeout:   time.Duration(cfg.LLMTimeout) * time.Second,
	}, nil
}

// Complete sends a prompt to the LLM and returns the completion
func (c *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if c.llm == nil {
		return "", errors.New("LLM client not initialized")
	}

	// Create a context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Log the prompt for debugging
	log.Printf("Sending prompt to LLM: %s", truncateForLogging(prompt))

	// Call the LLM for completion
	completion, err := c.llm.Call(timeoutCtx, prompt, llms.WithMaxTokens(c.maxTokens))
	if err != nil {
		log.Printf("LLM call failed: %v", err)
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	// Log the response for debugging
	log.Printf("Received LLM response: %s", truncateForLogging(completion))

	return completion, nil
}

// truncateForLogging truncates a string to a reasonable length for logging
func truncateForLogging(s string) string {
	const maxLength = 500
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "... [truncated]"
}
