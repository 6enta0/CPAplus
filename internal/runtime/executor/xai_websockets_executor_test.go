package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestXAIWebsocketsExecuteStreamSendsResponseCreateWithPreviousResponseID(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	capturedPayload := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer xai-token" {
			t.Errorf("Authorization = %q, want Bearer xai-token", got)
		}
		if got := r.Header.Get("x-grok-conv-id"); got != "execution-session-1" {
			t.Errorf("x-grok-conv-id = %q, want execution-session-1", got)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		_, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			t.Errorf("read upstream websocket message: %v", errRead)
			return
		}
		capturedPayload <- bytes.Clone(payload)
		completed := []byte(`{"type":"response.completed","response":{"id":"resp-xai-1","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)
		if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
			t.Errorf("write completed websocket message: %v", errWrite)
		}
	}))
	defer server.Close()

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "xai-auth",
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":   server.URL,
			"websockets": "true",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}
	req := cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","stream":true,"previous_response_id":"resp-prev","instructions":"system prompt","input":[{"type":"message","role":"user","content":"hello"}]}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: "execution-session-1",
		},
	}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	select {
	case payload := <-capturedPayload:
		if got := gjson.GetBytes(payload, "type").String(); got != "response.create" {
			t.Fatalf("type = %q, want response.create; payload=%s", got, payload)
		}
		if got := gjson.GetBytes(payload, "previous_response_id").String(); got != "resp-prev" {
			t.Fatalf("previous_response_id = %q, want resp-prev; payload=%s", got, payload)
		}
		if gjson.GetBytes(payload, "stream").Exists() {
			t.Fatalf("stream must be omitted for xAI websocket payload: %s", payload)
		}
		if gjson.GetBytes(payload, "instructions").Exists() {
			t.Fatalf("instructions must be omitted when previous_response_id is set: %s", payload)
		}
		if got := gjson.GetBytes(payload, "prompt_cache_key").String(); got != "execution-session-1" {
			t.Fatalf("prompt_cache_key = %q, want execution-session-1; payload=%s", got, payload)
		}
		if got := gjson.GetBytes(payload, "store").Bool(); !got {
			t.Fatalf("store = false, want true; payload=%s", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket payload")
	}

	select {
	case chunk, ok := <-result.Chunks:
		if !ok {
			t.Fatal("stream closed before completed chunk")
		}
		if chunk.Err != nil {
			t.Fatalf("chunk error = %v", chunk.Err)
		}
		if got := gjson.GetBytes(bytes.TrimSpace(chunk.Payload), "type").String(); got != "response.completed" {
			t.Fatalf("chunk type = %q, want response.completed; payload=%s", got, chunk.Payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for completed chunk")
	}
}

func TestXAIWebsocketTranscriptStateReplaysAfterIDMappingLoss(t *testing.T) {
	state := &xaiWebsocketIDState{downstreamToUpstream: map[string]string{}}
	state.recordTranscriptTurn(
		[]byte(`{"input":[{"type":"message","role":"user","content":"first"}]}`),
		[]byte(`{"response":{"output":[{"type":"message","role":"assistant","content":"answer"}]}}`),
	)
	mapper := &xaiWebsocketRequestIDMapper{state: state, downstreamPreviousID: "resp-client", upstreamPreviousID: ""}
	updated := mapper.upstreamRequestPayload([]byte(`{"previous_response_id":"resp-client","input":[{"type":"message","role":"user","content":"second"}]}`))
	if gjson.GetBytes(updated, "previous_response_id").Exists() {
		t.Fatalf("previous_response_id leaked after transcript fallback: %s", updated)
	}
	if got := gjson.GetBytes(updated, "input.0.content").String(); got != "first" {
		t.Fatalf("input.0.content = %q, want first; payload=%s", got, updated)
	}
	if got := gjson.GetBytes(updated, "input.1.content").String(); got != "answer" {
		t.Fatalf("input.1.content = %q, want answer; payload=%s", got, updated)
	}
	if got := gjson.GetBytes(updated, "input.2.content").String(); got != "second" {
		t.Fatalf("input.2.content = %q, want second; payload=%s", got, updated)
	}
}

func TestBuildXAIWebsocketCompactionPayloadReplacesPreviousState(t *testing.T) {
	payload, err := buildXAIWebsocketCompactionPayload(
		[]byte(`{"previous_response_id":"resp-old","input":[{"type":"compaction_trigger"}]}`),
		[]byte(`[{"type":"message","role":"user","content":"summary"}]`),
	)
	if err != nil {
		t.Fatalf("buildXAIWebsocketCompactionPayload() error = %v", err)
	}
	if gjson.GetBytes(payload, "previous_response_id").Exists() {
		t.Fatalf("previous_response_id was not removed: %s", payload)
	}
	if got := gjson.GetBytes(payload, "input.0.content").String(); got != "summary" {
		t.Fatalf("input.0.content = %q, want summary; payload=%s", got, payload)
	}
}

func TestXAIWebsocketsCompactionTriggerUsesRecordedTranscript(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	compactPayloads := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses/compact":
			body, errRead := io.ReadAll(r.Body)
			if errRead != nil {
				t.Errorf("read compact body: %v", errRead)
				return
			}
			compactPayloads <- bytes.Clone(body)
			_, _ = w.Write([]byte(`{"id":"resp_compact","output":[{"type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
		case "/responses":
			conn, errUpgrade := upgrader.Upgrade(w, r, nil)
			if errUpgrade != nil {
				t.Errorf("upgrade websocket: %v", errUpgrade)
				return
			}
			defer func() { _ = conn.Close() }()
			for i := 0; i < 2; i++ {
				_, _, errRead := conn.ReadMessage()
				if errRead != nil {
					t.Errorf("read websocket message: %v", errRead)
					return
				}
				responseID := "resp-first"
				if i == 1 {
					responseID = "resp-second"
				}
				response := `{"type":"response.completed","response":{"id":"` + responseID + `","output":[{"type":"message","id":"out-` + responseID + `","role":"assistant","content":"answer"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`
				if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(response)); errWrite != nil {
					t.Errorf("write response: %v", errWrite)
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.idStore = &xaiWebsocketIDStateStore{sessions: make(map[string]*xaiWebsocketIDState)}
	auth := &cliproxyauth.Auth{ID: "xai-compact-auth", Provider: "xai", Attributes: map[string]string{"base_url": server.URL, "websockets": "true"}, Metadata: map[string]any{"access_token": "token"}}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "compact-session"}}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())

	first, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":[{"type":"message","role":"user","content":"first"}]}`)}, opts)
	if err != nil {
		t.Fatalf("first ExecuteStream() error = %v", err)
	}
	for chunk := range first.Chunks {
		if chunk.Err != nil {
			t.Fatalf("first chunk error = %v", chunk.Err)
		}
	}

	compact, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","input":[{"type":"compaction_trigger"}]}`)}, opts)
	if err != nil {
		t.Fatalf("compact ExecuteStream() error = %v", err)
	}
	for chunk := range compact.Chunks {
		if chunk.Err != nil {
			t.Fatalf("compact chunk error = %v", chunk.Err)
		}
	}
	select {
	case body := <-compactPayloads:
		input := gjson.GetBytes(body, "input")
		if !input.IsArray() || len(input.Array()) != 2 {
			t.Fatalf("compact input = %s, want two transcript items", input.Raw)
		}
		if input.Array()[0].Get("role").String() != "user" || input.Array()[1].Get("role").String() != "assistant" {
			t.Fatalf("unexpected compact transcript: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for compact request")
	}

	next, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"model":"grok-4.3","previous_response_id":"resp_compact","input":[{"type":"message","role":"user","content":"second"}]}`)}, opts)
	if err != nil {
		t.Fatalf("post-compact ExecuteStream() error = %v", err)
	}
	for chunk := range next.Chunks {
		if chunk.Err != nil {
			t.Fatalf("post-compact chunk error = %v", chunk.Err)
		}
	}
}

func TestXAIWebsocketsAuthChangeReplaysTranscriptWithoutPreviousID(t *testing.T) {
	newWebsocketServer := func(responseID string, captured chan<- []byte) *httptest.Server {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, errUpgrade := upgrader.Upgrade(w, r, nil)
			if errUpgrade != nil {
				t.Errorf("upgrade websocket: %v", errUpgrade)
				return
			}
			defer func() { _ = conn.Close() }()
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				t.Errorf("read websocket message: %v", errRead)
				return
			}
			captured <- bytes.Clone(payload)
			completed := `{"type":"response.completed","response":{"id":"` + responseID + `","output":[{"type":"message","role":"assistant","content":"answer"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`
			if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(completed)); errWrite != nil {
				t.Errorf("write websocket response: %v", errWrite)
			}
		}))
	}
	firstPayloads := make(chan []byte, 1)
	secondPayloads := make(chan []byte, 1)
	firstServer := newWebsocketServer("resp-first", firstPayloads)
	defer firstServer.Close()
	secondServer := newWebsocketServer("resp-second", secondPayloads)
	defer secondServer.Close()

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.idStore = &xaiWebsocketIDStateStore{sessions: make(map[string]*xaiWebsocketIDState)}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: true, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "failover-session"}}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	firstAuth := &cliproxyauth.Auth{ID: "auth-a", Provider: "xai", Attributes: map[string]string{"base_url": firstServer.URL, "websockets": "true"}}
	secondAuth := &cliproxyauth.Auth{ID: "auth-b", Provider: "xai", Attributes: map[string]string{"base_url": secondServer.URL, "websockets": "true"}}

	first, err := exec.ExecuteStream(ctx, firstAuth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"input":[{"type":"message","role":"user","content":"first"}]}`)}, opts)
	if err != nil {
		t.Fatalf("first ExecuteStream() error = %v", err)
	}
	for chunk := range first.Chunks {
		if chunk.Err != nil {
			t.Fatalf("first chunk error = %v", chunk.Err)
		}
	}
	<-firstPayloads

	second, err := exec.ExecuteStream(ctx, secondAuth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: []byte(`{"previous_response_id":"resp-first","input":[{"type":"message","role":"user","content":"second"}]}`)}, opts)
	if err != nil {
		t.Fatalf("second ExecuteStream() error = %v", err)
	}
	for chunk := range second.Chunks {
		if chunk.Err != nil {
			t.Fatalf("second chunk error = %v", chunk.Err)
		}
	}
	select {
	case payload := <-secondPayloads:
		if gjson.GetBytes(payload, "previous_response_id").Exists() {
			t.Fatalf("previous_response_id leaked across auth change: %s", payload)
		}
		input := gjson.GetBytes(payload, "input")
		if !input.IsArray() || len(input.Array()) != 3 {
			t.Fatalf("failover input = %s, want full three-item transcript", input.Raw)
		}
		if input.Array()[0].Get("content").String() != "first" || input.Array()[2].Get("content").String() != "second" {
			t.Fatalf("failover transcript order is wrong: %s", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for failover request")
	}
}

func TestXAIWebsocketsExecuteStreamRewritesRepeatedResponseIDForDownstream(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	capturedPreviousIDs := make(chan string, 3)
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		for i := 0; i < 3; i++ {
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				t.Errorf("read upstream websocket message: %v", errRead)
				return
			}
			previousID := gjson.GetBytes(payload, "previous_response_id").String()
			capturedPreviousIDs <- previousID
			completed := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-real","previous_response_id":%q,"output":[{"id":"rs_resp-real","type":"reasoning","status":"completed"}],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`, previousID))
			if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
				t.Errorf("write completed websocket message: %v", errWrite)
				return
			}
		}
		<-releaseServer
	}))
	defer server.Close()
	defer close(releaseServer)

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.idStore = &xaiWebsocketIDStateStore{sessions: make(map[string]*xaiWebsocketIDState)}
	auth := &cliproxyauth.Auth{
		ID:       "xai-auth-id-map",
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":   server.URL,
			"websockets": "true",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: "xai-id-map-session",
		},
	}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())

	runRequest := func(previousID string) (string, string, string) {
		body := []byte(`{"model":"grok-4.3","input":[{"type":"message","role":"user","content":"hello"}]}`)
		if previousID != "" {
			body = []byte(fmt.Sprintf(`{"model":"grok-4.3","previous_response_id":%q,"input":[{"type":"function_call_output","call_id":"call-1","output":"ok"}]}`, previousID))
		}
		result, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "grok-4.3", Payload: body}, opts)
		if err != nil {
			t.Fatalf("ExecuteStream() error = %v", err)
		}
		select {
		case chunk, ok := <-result.Chunks:
			if !ok {
				t.Fatal("stream closed before completed chunk")
			}
			if chunk.Err != nil {
				t.Fatalf("chunk error = %v", chunk.Err)
			}
			payload := bytes.TrimSpace(chunk.Payload)
			return gjson.GetBytes(payload, "response.id").String(),
				gjson.GetBytes(payload, "response.output.0.id").String(),
				gjson.GetBytes(payload, "response.previous_response_id").String()
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for completed chunk")
		}
		return "", "", ""
	}

	firstDownstreamID, firstOutputID, firstResponsePrevious := runRequest("")
	if firstDownstreamID != "resp-real" {
		t.Fatalf("first downstream id = %q, want resp-real", firstDownstreamID)
	}
	if firstOutputID != "rs_resp-real" {
		t.Fatalf("first output item id = %q, want rs_resp-real", firstOutputID)
	}
	if firstResponsePrevious != "" {
		t.Fatalf("first response previous_response_id = %q, want empty", firstResponsePrevious)
	}
	firstUpstreamPrevious := <-capturedPreviousIDs
	if firstUpstreamPrevious != "" {
		t.Fatalf("first upstream previous_response_id = %q, want empty", firstUpstreamPrevious)
	}

	secondDownstreamID, secondOutputID, secondResponsePrevious := runRequest(firstDownstreamID)
	if secondDownstreamID == "" || secondDownstreamID == "resp-real" {
		t.Fatalf("second downstream id = %q, want synthetic id different from resp-real", secondDownstreamID)
	}
	if secondOutputID == "rs_resp-real" || !strings.Contains(secondOutputID, secondDownstreamID) {
		t.Fatalf("second output item id = %q, want rewritten id containing %q", secondOutputID, secondDownstreamID)
	}
	if secondResponsePrevious != firstDownstreamID {
		t.Fatalf("second response previous_response_id = %q, want %q", secondResponsePrevious, firstDownstreamID)
	}
	secondUpstreamPrevious := <-capturedPreviousIDs
	if secondUpstreamPrevious != "resp-real" {
		t.Fatalf("second upstream previous_response_id = %q, want resp-real", secondUpstreamPrevious)
	}

	thirdDownstreamID, thirdOutputID, thirdResponsePrevious := runRequest(secondDownstreamID)
	if thirdDownstreamID == "" || thirdDownstreamID == "resp-real" || thirdDownstreamID == secondDownstreamID {
		t.Fatalf("third downstream id = %q, want a new synthetic id", thirdDownstreamID)
	}
	if thirdOutputID == "rs_resp-real" || !strings.Contains(thirdOutputID, thirdDownstreamID) {
		t.Fatalf("third output item id = %q, want rewritten id containing %q", thirdOutputID, thirdDownstreamID)
	}
	if thirdResponsePrevious != secondDownstreamID {
		t.Fatalf("third response previous_response_id = %q, want %q", thirdResponsePrevious, secondDownstreamID)
	}
	thirdUpstreamPrevious := <-capturedPreviousIDs
	if thirdUpstreamPrevious != "resp-real" {
		t.Fatalf("third upstream previous_response_id = %q, want resp-real", thirdUpstreamPrevious)
	}
}

func TestBuildXAIWebsocketRequestBodySetsStoreAndKeepsPromptCacheKey(t *testing.T) {
	body := []byte(`{"model":"grok-4.3","stream":true,"stream_options":{"include_usage":true},"background":true,"prompt_cache_key":"cache-1","previous_response_id":"resp-prev","instructions":"system prompt","input":[{"type":"message","role":"user","content":"hello"}]}`)

	payload := buildXAIWebsocketRequestBody(body)

	if got := gjson.GetBytes(payload, "type").String(); got != "response.create" {
		t.Fatalf("type = %q, want response.create; payload=%s", got, payload)
	}
	if gjson.GetBytes(payload, "stream").Exists() {
		t.Fatalf("stream must be omitted for xAI websocket payload: %s", payload)
	}
	if gjson.GetBytes(payload, "stream_options").Exists() {
		t.Fatalf("stream_options must be omitted for xAI websocket payload: %s", payload)
	}
	if gjson.GetBytes(payload, "background").Exists() {
		t.Fatalf("background must be omitted for xAI websocket payload: %s", payload)
	}
	if got := gjson.GetBytes(payload, "prompt_cache_key").String(); got != "cache-1" {
		t.Fatalf("prompt_cache_key = %q, want cache-1; payload=%s", got, payload)
	}
	if got := gjson.GetBytes(payload, "store").Bool(); !got {
		t.Fatalf("store = false, want true; payload=%s", payload)
	}
	if gjson.GetBytes(payload, "instructions").Exists() {
		t.Fatalf("instructions must be omitted when previous_response_id is set: %s", payload)
	}
}

func TestXAIWebsocketsExecuteStreamCompletesGenerateFalseWarmup(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	capturedPayload := make(chan []byte, 1)
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		_, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			t.Errorf("read upstream websocket message: %v", errRead)
			return
		}
		capturedPayload <- bytes.Clone(payload)
		created := []byte(`{"type":"response.created","response":{"id":"resp-warmup-1","object":"response","status":"in_progress","output":[]}}`)
		if errWrite := conn.WriteMessage(websocket.TextMessage, created); errWrite != nil {
			t.Errorf("write created websocket message: %v", errWrite)
			return
		}
		<-releaseServer
	}))
	defer server.Close()
	defer close(releaseServer)

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "xai-auth-warmup",
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":   server.URL,
			"websockets": "true",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}
	req := cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","generate":false,"input":[{"type":"message","role":"user","content":"warm up"}]}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
	}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	select {
	case payload := <-capturedPayload:
		if got := gjson.GetBytes(payload, "generate").Bool(); got {
			t.Fatalf("generate = true, want false; payload=%s", payload)
		}
		if got := gjson.GetBytes(payload, "type").String(); got != "response.create" {
			t.Fatalf("type = %q, want response.create; payload=%s", got, payload)
		}
		if got := gjson.GetBytes(payload, "store").Bool(); !got {
			t.Fatalf("store = false, want true; payload=%s", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket payload")
	}

	var gotTypes []string
	for {
		select {
		case chunk, ok := <-result.Chunks:
			if !ok {
				if len(gotTypes) != 2 {
					t.Fatalf("event types = %v, want response.created and response.completed", gotTypes)
				}
				return
			}
			if chunk.Err != nil {
				t.Fatalf("chunk error = %v", chunk.Err)
			}
			gotTypes = append(gotTypes, gjson.GetBytes(bytes.TrimSpace(chunk.Payload), "type").String())
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for warmup stream to close; event types so far: %v", gotTypes)
		}
	}
}

func TestXAIWebsocketsExecuteStreamStopsOnBareErrorPayload(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		if _, _, errRead := conn.ReadMessage(); errRead != nil {
			t.Errorf("read upstream websocket message: %v", errRead)
			return
		}
		payload := []byte(`{"error":{"message":"Request validation error: {\"code\":\"400\",\"error\":\"Argument not supported: instructions and previous_response_id together\"}","type":"api_error"}}`)
		if errWrite := conn.WriteMessage(websocket.TextMessage, payload); errWrite != nil {
			t.Errorf("write error websocket message: %v", errWrite)
			return
		}
		<-releaseServer
	}))
	defer server.Close()
	defer close(releaseServer)

	exec := NewXAIWebsocketsExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "xai-auth-error",
		Provider: "xai",
		Attributes: map[string]string{
			"base_url":   server.URL,
			"websockets": "true",
		},
		Metadata: map[string]any{"access_token": "xai-token"},
	}
	req := cliproxyexecutor.Request{
		Model:   "grok-4.3",
		Payload: []byte(`{"model":"grok-4.3","input":"hello"}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse,
	}
	ctx := cliproxyexecutor.WithDownstreamWebsocket(context.Background())

	result, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	select {
	case chunk, ok := <-result.Chunks:
		if !ok {
			t.Fatal("stream closed before error chunk")
		}
		if chunk.Err == nil {
			t.Fatalf("chunk error = nil, want upstream error; payload=%s", chunk.Payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bare upstream error")
	}
}
