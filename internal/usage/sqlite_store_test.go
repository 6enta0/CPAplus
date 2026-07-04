package usage

import (
	"fmt"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestBuildOpenAICompatAuthIndexMatchesPrefixFreeAuthIdentity(t *testing.T) {
	t.Parallel()

	name := "Shared"
	baseURL := "https://one.example/v1"
	apiKey := "key-a"
	proxyURL := "http://proxy.example.com:8080"
	identityProviderKey := openAICompatIdentityProviderKey(name, baseURL)
	identityKind := fmt.Sprintf("openai-compatibility:%s", identityProviderKey)
	identitySource := fmt.Sprintf("config:%s[%s]", identityProviderKey, stableUsageToken(identityKind, apiKey, proxyURL))

	got := buildOpenAICompatAuthIndex(identityProviderKey, name, baseURL, apiKey, proxyURL, identitySource, "", false)
	auth := &coreauth.Auth{
		Provider: identityProviderKey,
		ProxyURL: proxyURL,
		Attributes: map[string]string{
			"identity_provider_key": identityProviderKey,
			"identity_source":       identitySource,
			"provider_key":          openAICompatProviderKey(name, "deepseek", baseURL),
			"compat_name":           name,
			"base_url":              baseURL,
			"api_key":               apiKey,
			"prefix":                "deepseek",
		},
	}
	want := auth.EnsureIndex()

	if got == "" {
		t.Fatal("auth index should not be empty")
	}
	if got != want {
		t.Fatalf("auth index = %q, want %q", got, want)
	}
}
