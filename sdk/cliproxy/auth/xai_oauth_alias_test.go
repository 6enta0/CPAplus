package auth

import "testing"

func TestOAuthModelAliasChannelXAI(t *testing.T) {
	if got := OAuthModelAliasChannel("xai", "oauth"); got != "xai" {
		t.Fatalf("OAuthModelAliasChannel(xai, oauth) = %q", got)
	}
	if got := OAuthModelAliasChannel("xai", "apikey"); got != "" {
		t.Fatalf("OAuthModelAliasChannel(xai, apikey) = %q, want empty", got)
	}
}
