// Package constant defines provider name constants used throughout the CLI Proxy API.
// These constants identify different AI service providers and their variants,
// ensuring consistent naming across the application.
package constant

const (
	// Gemini represents the Google Gemini provider identifier.
	Gemini = "gemini"

	// GeminiInteractions identifies the native Google Interactions provider.
	GeminiInteractions = "gemini-interactions"

	// GeminiCLI represents the Google Gemini CLI provider identifier.
	GeminiCLI = "gemini-cli"

	// Codex represents the OpenAI Codex provider identifier.
	Codex = "codex"

	// XAI represents the native xAI Grok provider identifier.
	XAI = "xai"

	// Claude represents the Anthropic Claude provider identifier.
	Claude = "claude"

	// OpenAI represents the OpenAI provider identifier.
	OpenAI = "openai"

	// OpenaiResponse represents the OpenAI response format identifier.
	OpenaiResponse = "openai-response"

	// Antigravity represents the Antigravity response format identifier.
	Antigravity = "antigravity"

	// Interactions identifies the Google Interactions protocol.
	Interactions = "interactions"
)
