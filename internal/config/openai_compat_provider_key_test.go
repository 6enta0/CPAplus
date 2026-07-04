package config

import "testing"

func TestOpenAICompatibilityProviderKey(t *testing.T) {
	got := OpenAICompatibilityProviderKey(" Sakura-Old ", "deepseek", " https://example.com/v1 ")
	want := "sakura-old|prefix=deepseek|base=https://example.com/v1"
	if got != want {
		t.Fatalf("OpenAICompatibilityProviderKey() = %q, want %q", got, want)
	}
}

func TestOpenAICompatibilityProviderKey_EmptyNameFallsBack(t *testing.T) {
	got := OpenAICompatibilityProviderKey("", "", "https://example.com/v1")
	want := "openai-compatibility|base=https://example.com/v1"
	if got != want {
		t.Fatalf("OpenAICompatibilityProviderKey() = %q, want %q", got, want)
	}
}

func TestOpenAICompatibilityIdentityProviderKeyIgnoresPrefix(t *testing.T) {
	entry := OpenAICompatibility{
		Name:    " Sakura-Old ",
		Prefix:  "deepseek",
		BaseURL: " https://example.com/v1 ",
	}
	got := OpenAICompatibilityIdentityProviderKey(entry)
	want := "sakura-old|base=https://example.com/v1"
	if got != want {
		t.Fatalf("OpenAICompatibilityIdentityProviderKey() = %q, want %q", got, want)
	}
}

func TestResolveOpenAICompatibilityEntry_PrefersCompositeProviderKey(t *testing.T) {
	entries := []OpenAICompatibility{
		{
			Name:    "shared",
			Prefix:  "deepseek",
			BaseURL: "https://deepseek.example/v1",
		},
		{
			Name:    "shared",
			Prefix:  "glm",
			BaseURL: "https://glm.example/v1",
		},
	}

	providerKey := OpenAICompatibilityProviderKey("shared", "glm", "https://glm.example/v1")
	got := ResolveOpenAICompatibilityEntry(entries, providerKey, "shared", providerKey)
	if got == nil {
		t.Fatal("ResolveOpenAICompatibilityEntry() returned nil")
	}
	if got.Prefix != "glm" {
		t.Fatalf("ResolveOpenAICompatibilityEntry() prefix = %q, want %q", got.Prefix, "glm")
	}
}
