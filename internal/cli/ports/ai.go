package ports

import "context"

// CommandAIOptions are the only AI configuration inputs accepted by CLI
// commands. A complete explicit configuration takes precedence over a saved
// configuration selected by ID.
type CommandAIOptions struct {
	AIConfigID  int
	BaseURL     string
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float64
	Timeout     int
}

// CommandAIClient is the minimal streaming surface used by the ai command.
type CommandAIClient interface {
	NewChatStreamLite(stock, stockCode, question string, thinking bool) <-chan map[string]any
}

// CommandAIResolver selects saved or explicit configuration and constructs a
// client without exposing the legacy provider implementation to the CLI.
type CommandAIResolver interface {
	ResolveCommandAI(context.Context, CommandAIOptions) (CommandAIClient, error)
}
