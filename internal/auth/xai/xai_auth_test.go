package xai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateOAuthEndpointRejectsNonXAIOrigin(t *testing.T) {
	if _, errValidate := ValidateOAuthEndpoint("https://auth.x.ai/oauth2/token", "token_endpoint"); errValidate != nil {
		t.Fatalf("ValidateOAuthEndpoint(xai) error = %v", errValidate)
	}
	if _, errValidate := ValidateOAuthEndpoint("http://auth.x.ai/oauth2/token", "token_endpoint"); errValidate == nil {
		t.Fatal("expected non-HTTPS endpoint to be rejected")
	}
	if _, errValidate := ValidateOAuthEndpoint("https://evil.example/oauth/token", "token_endpoint"); errValidate == nil {
		t.Fatal("expected non-xAI endpoint to be rejected")
	}
}

func TestRequestDeviceCodePostsClientIDAndScope(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if errParse := request.ParseForm(); errParse != nil {
			t.Fatalf("ParseForm() error = %v", errParse)
		}
		received = request.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "device-1", "user_code": "CODE-1",
			"verification_uri": "https://accounts.x.ai/device", "expires_in": 120, "interval": 5,
		})
	}))
	defer server.Close()

	deviceCode, errRequest := NewXAIAuth(nil).RequestDeviceCode(context.Background(), server.URL, "https://auth.x.ai/token")
	if errRequest != nil {
		t.Fatalf("RequestDeviceCode() error = %v", errRequest)
	}
	if deviceCode.DeviceCode != "device-1" || deviceCode.TokenEndpoint != "https://auth.x.ai/token" {
		t.Fatalf("device response = %#v", deviceCode)
	}
	if received.Get("client_id") != ClientID || received.Get("scope") != Scope {
		t.Fatalf("client_id/scope = %q/%q", received.Get("client_id"), received.Get("scope"))
	}
}

func TestExchangeDeviceCodeHandlesPendingSlowDownAndSuccess(t *testing.T) {
	responses := []map[string]any{
		{"error": "authorization_pending"},
		{"error": "slow_down"},
		{"access_token": "access", "refresh_token": "refresh", "id_token": fakeJWTClaims("user@x.ai", "subject-1"), "expires_in": 3600},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if errParse := request.ParseForm(); errParse != nil {
			t.Fatalf("ParseForm() error = %v", errParse)
		}
		if request.PostForm.Get("grant_type") != DeviceCodeGrantType || request.PostForm.Get("device_code") != "device-1" {
			t.Fatalf("unexpected form: %v", request.PostForm)
		}
		response := responses[0]
		responses = responses[1:]
		if response["error"] != nil {
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := NewXAIAuth(nil)
	interval := 5 * time.Second
	if token, errPoll, next, keepPolling := auth.exchangeDeviceCode(context.Background(), server.URL, "device-1", interval); token != nil || errPoll != nil || !keepPolling || next != interval {
		t.Fatalf("pending result = %#v/%v/%v/%t", token, errPoll, next, keepPolling)
	}
	if token, errPoll, next, keepPolling := auth.exchangeDeviceCode(context.Background(), server.URL, "device-1", interval); token != nil || errPoll != nil || !keepPolling || next != 10*time.Second {
		t.Fatalf("slow_down result = %#v/%v/%v/%t", token, errPoll, next, keepPolling)
	}
	token, errPoll, _, keepPolling := auth.exchangeDeviceCode(context.Background(), server.URL, "device-1", interval)
	if errPoll != nil || keepPolling || token == nil {
		t.Fatalf("success result = %#v/%v/%t", token, errPoll, keepPolling)
	}
	if token.Email != "user@x.ai" || token.Subject != "subject-1" || token.Expire == "" {
		t.Fatalf("token identity/expiry = %#v", token)
	}
}

func TestExchangeDeviceCodeStopsOnAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	}))
	defer server.Close()
	_, errPoll, _, keepPolling := NewXAIAuth(nil).exchangeDeviceCode(context.Background(), server.URL, "device-1", time.Second)
	if errPoll == nil || keepPolling || !strings.Contains(errPoll.Error(), "authorization denied") {
		t.Fatalf("error/keepPolling = %v/%t", errPoll, keepPolling)
	}
}

func TestRefreshTokensPostsRefreshGrant(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if errParse := request.ParseForm(); errParse != nil {
			t.Fatalf("ParseForm() error = %v", errParse)
		}
		received = request.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer server.Close()
	token, errRefresh := NewXAIAuth(nil).RefreshTokens(context.Background(), "old-refresh", server.URL)
	if errRefresh != nil {
		t.Fatalf("RefreshTokens() error = %v", errRefresh)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "" {
		t.Fatalf("token = %#v", token)
	}
	if received.Get("grant_type") != "refresh_token" || received.Get("client_id") != ClientID || received.Get("refresh_token") != "old-refresh" {
		t.Fatalf("refresh form = %v", received)
	}
}

func TestTokenStorageAndCredentialFileName(t *testing.T) {
	if got := CredentialFileName("user+test@x.ai", "subject"); got != "xai-user-test@x.ai.json" {
		t.Fatalf("CredentialFileName() = %q", got)
	}
	path := filepath.Join(t.TempDir(), "xai-user.json")
	storage := &TokenStorage{AccessToken: "access", RefreshToken: "refresh", BaseURL: DefaultAPIBaseURL}
	storage.SetMetadata(map[string]any{"disabled": true})
	if errSave := storage.SaveTokenToFile(path); errSave != nil {
		t.Fatalf("SaveTokenToFile() error = %v", errSave)
	}
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("ReadFile() error = %v", errRead)
	}
	var metadata map[string]any
	if errUnmarshal := json.Unmarshal(data, &metadata); errUnmarshal != nil {
		t.Fatalf("json.Unmarshal() error = %v", errUnmarshal)
	}
	if metadata["type"] != "xai" || metadata["auth_kind"] != "oauth" || metadata["disabled"] != true {
		t.Fatalf("stored metadata = %#v", metadata)
	}
}

func fakeJWTClaims(email, subject string) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(map[string]string{"email": email, "sub": subject})
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
