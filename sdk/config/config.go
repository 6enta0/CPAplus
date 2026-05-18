// Package config provides the public SDK configuration API.
//
// It re-exports the server configuration types and helpers so external projects can
// embed CLIProxyAPI without importing internal packages.
package config

import internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"

type SDKConfig = internalconfig.SDKConfig

type Config = internalconfig.Config

type StreamingConfig = internalconfig.StreamingConfig
type TLSConfig = internalconfig.TLSConfig
type RemoteManagement = internalconfig.RemoteManagement
type AmpCode = internalconfig.AmpCode
type OAuthModelAlias = internalconfig.OAuthModelAlias
type PayloadConfig = internalconfig.PayloadConfig
type PayloadRule = internalconfig.PayloadRule
type PayloadFilterRule = internalconfig.PayloadFilterRule
type PayloadModelRule = internalconfig.PayloadModelRule

type GeminiKey = internalconfig.GeminiKey
type CodexKey = internalconfig.CodexKey
type ClaudeKey = internalconfig.ClaudeKey
type VertexCompatKey = internalconfig.VertexCompatKey
type VertexCompatModel = internalconfig.VertexCompatModel
type OpenAICompatibility = internalconfig.OpenAICompatibility
type OpenAICompatibilityAPIKey = internalconfig.OpenAICompatibilityAPIKey
type OpenAICompatibilityModel = internalconfig.OpenAICompatibilityModel

type TLS = internalconfig.TLSConfig

const (
	DefaultPanelGitHubRepository = internalconfig.DefaultPanelGitHubRepository
)

func LoadConfig(configFile string) (*Config, error) { return internalconfig.LoadConfig(configFile) }

func LoadConfigOptional(configFile string, optional bool) (*Config, error) {
	return internalconfig.LoadConfigOptional(configFile, optional)
}

func SaveConfigPreserveComments(configFile string, cfg *Config) error {
	return internalconfig.SaveConfigPreserveComments(configFile, cfg)
}

func SaveConfigPreserveCommentsUpdateNestedScalar(configFile string, path []string, value string) error {
	return internalconfig.SaveConfigPreserveCommentsUpdateNestedScalar(configFile, path, value)
}

func NormalizeCommentIndentation(data []byte) []byte {
	return internalconfig.NormalizeCommentIndentation(data)
}

func OpenAICompatibilityProviderName(name string) string {
	return internalconfig.OpenAICompatibilityProviderName(name)
}

func OpenAICompatibilityProviderKey(name, prefix, baseURL string) string {
	return internalconfig.OpenAICompatibilityProviderKey(name, prefix, baseURL)
}

func OpenAICompatibilityProviderKeyForEntry(entry OpenAICompatibility) string {
	return internalconfig.OpenAICompatibilityProviderKeyForEntry(entry)
}

func OpenAICompatibilityEntryMatches(entry OpenAICompatibility, providerKey, compatName, authProvider string) bool {
	return internalconfig.OpenAICompatibilityEntryMatches(entry, providerKey, compatName, authProvider)
}

func ResolveOpenAICompatibilityEntry(entries []OpenAICompatibility, providerKey, compatName, authProvider string) *OpenAICompatibility {
	return internalconfig.ResolveOpenAICompatibilityEntry(entries, providerKey, compatName, authProvider)
}
