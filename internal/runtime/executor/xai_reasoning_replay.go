package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	internalcache "github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type xaiReasoningReplayScope struct {
	modelName  string
	sessionKey string
}

var getXAIReasoningReplayItemsRequired = internalcache.GetXAIReasoningReplayItemsRequired

func (s xaiReasoningReplayScope) valid() bool {
	return strings.TrimSpace(s.modelName) != "" && strings.TrimSpace(s.sessionKey) != ""
}

func applyXAIReasoningReplayCacheRequired(ctx context.Context, from sdktranslator.Format, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, body []byte) ([]byte, xaiReasoningReplayScope, error) {
	scope := xaiReasoningReplayScopeFromRequest(ctx, from, req, opts, body)
	if !scope.valid() {
		return body, scope, nil
	}
	items, ok, errReplay := getXAIReasoningReplayItemsRequired(ctx, scope.modelName, scope.sessionKey)
	if errReplay != nil || !ok {
		if errReplay != nil {
			log.Debugf("xai reasoning replay cache read failed: %v", errReplay)
		}
		return body, scope, nil
	}
	items = filterXAIReasoningReplayItemsForInput(body, items)
	if len(items) == 0 {
		return body, scope, nil
	}
	updated, ok := insertXAIReasoningReplayItems(body, items)
	if !ok {
		return body, scope, nil
	}
	return updated, scope, nil
}

func xaiReasoningReplayScopeFromRequest(ctx context.Context, from sdktranslator.Format, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, body []byte) xaiReasoningReplayScope {
	if !xaiReasoningReplayEnabledForSource(from) {
		return xaiReasoningReplayScope{}
	}
	if cliproxyexecutor.DownstreamWebsocket(ctx) && strings.TrimSpace(gjson.GetBytes(req.Payload, "previous_response_id").String()) != "" {
		return xaiReasoningReplayScope{}
	}
	sessionKey := xaiReasoningReplaySessionKey(ctx, req, opts, body)
	if sessionKey == "" {
		return xaiReasoningReplayScope{}
	}
	if strings.HasPrefix(sessionKey, "execution:") {
		return xaiReasoningReplayScope{modelName: thinking.ParseSuffix(req.Model).ModelName, sessionKey: sessionKey}
	}
	apiKey := strings.TrimSpace(helps.APIKeyFromContext(ctx))
	if apiKey == "" {
		return xaiReasoningReplayScope{}
	}
	sum := sha256.Sum256([]byte(apiKey))
	sessionKey = "caller:" + hex.EncodeToString(sum[:8]) + ":" + sessionKey
	return xaiReasoningReplayScope{modelName: thinking.ParseSuffix(req.Model).ModelName, sessionKey: sessionKey}
}

func xaiReasoningReplayEnabledForSource(from sdktranslator.Format) bool {
	return strings.EqualFold(strings.TrimSpace(from.String()), sdktranslator.FormatClaude.String()) ||
		strings.EqualFold(strings.TrimSpace(from.String()), sdktranslator.FormatOpenAIResponse.String())
}

func xaiReasoningReplaySessionKey(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, body []byte) string {
	if value := xaiReplayMetadataString(opts.Metadata, cliproxyexecutor.ExecutionSessionMetadataKey); value != "" {
		return "execution:" + value
	}
	if value := xaiReplayMetadataString(req.Metadata, cliproxyexecutor.ExecutionSessionMetadataKey); value != "" {
		return "execution:" + value
	}
	for _, payload := range [][]byte{body, req.Payload} {
		if value := strings.TrimSpace(gjson.GetBytes(payload, "prompt_cache_key").String()); value != "" {
			return "prompt-cache:" + value
		}
		if value := strings.TrimSpace(gjson.GetBytes(payload, "client_metadata.x-codex-window-id").String()); value != "" {
			return "window:" + value
		}
		if raw := strings.TrimSpace(gjson.GetBytes(payload, "metadata.user_id").String()); raw != "" {
			if sessionID := strings.TrimSpace(gjson.Get(raw, "session_id").String()); sessionID != "" {
				return "claude:" + sessionID
			}
			return "claude:" + raw
		}
	}
	for _, header := range []string{"X-Codex-Window-Id", "Session_id", "session_id", "Session-Id", "Conversation_id"} {
		if value := strings.TrimSpace(opts.Headers.Get(header)); value != "" {
			return "header:" + strings.ToLower(header) + ":" + value
		}
	}
	_ = ctx
	return ""
}

func xaiReplayMetadataString(metadata map[string]any, key string) string {
	if raw := metadata[key]; raw != nil {
		if value, ok := raw.(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func filterXAIReasoningReplayItemsForInput(body []byte, items [][]byte) [][]byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil
	}
	inputItems := input.Array()
	lastAssistant, hasLastAssistant := xaiReplayLastAssistantMessage(inputItems)
	cachedAssistant, hasCachedAssistant := xaiReplayAssistantMessage(items)
	assistantMatches := hasLastAssistant && hasCachedAssistant && xaiReplayAssistantContentEqual(lastAssistant.Get("content"), cachedAssistant.Get("content"))
	if hasLastAssistant && hasCachedAssistant && !assistantMatches {
		return nil
	}
	existingCalls := make(map[string]bool)
	existingOutputs := make(map[string]bool)
	for _, item := range inputItems {
		typ := strings.TrimSpace(item.Get("type").String())
		if typ == "function_call_output" || typ == "custom_tool_call_output" {
			if id := strings.TrimSpace(item.Get("call_id").String()); id != "" {
				existingOutputs[id] = true
			}
		}
		if key := xaiReplayToolCallKey(item); key != "" {
			existingCalls[key] = true
		}
	}
	filtered := make([][]byte, 0, len(items))
	for _, raw := range items {
		item := gjson.ParseBytes(raw)
		switch strings.TrimSpace(item.Get("type").String()) {
		case "reasoning":
			duplicate := false
			for _, inputItem := range inputItems {
				if inputItem.Get("type").String() == "reasoning" && inputItem.Get("encrypted_content").String() == item.Get("encrypted_content").String() {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
		case "message":
			if assistantMatches {
				continue
			}
		case "function_call", "custom_tool_call":
			key := xaiReplayToolCallKey(item)
			if key == "" || existingCalls[key] || !existingOutputs[strings.TrimSpace(item.Get("call_id").String())] {
				continue
			}
			existingCalls[key] = true
		default:
			continue
		}
		filtered = append(filtered, raw)
	}
	return filtered
}

func insertXAIReasoningReplayItems(body []byte, replayItems [][]byte) ([]byte, bool) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || len(replayItems) == 0 {
		return body, false
	}
	items := input.Array()
	insertAt := xaiReasoningReplayInsertIndex(items, replayItems)
	merged := make([]string, 0, len(items)+len(replayItems))
	for i, item := range items {
		if i == insertAt {
			for _, replay := range replayItems {
				merged = append(merged, string(replay))
			}
		}
		merged = append(merged, item.Raw)
	}
	if insertAt == len(items) {
		for _, replay := range replayItems {
			merged = append(merged, string(replay))
		}
	}
	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(merged, ",")+"]"))
	return updated, err == nil
}

func xaiReasoningReplayInsertIndex(inputItems []gjson.Result, replayItems [][]byte) int {
	replayCallIDs := make(map[string]bool)
	for _, replayItem := range replayItems {
		item := gjson.ParseBytes(replayItem)
		typ := strings.TrimSpace(item.Get("type").String())
		if typ == "function_call" || typ == "custom_tool_call" {
			if id := strings.TrimSpace(item.Get("call_id").String()); id != "" {
				replayCallIDs[id] = true
			}
		}
	}
	if len(replayCallIDs) > 0 {
		for index, item := range inputItems {
			typ := strings.TrimSpace(item.Get("type").String())
			if typ != "function_call_output" && typ != "custom_tool_call_output" {
				continue
			}
			if replayCallIDs[strings.TrimSpace(item.Get("call_id").String())] {
				return index
			}
		}
	}
	for index := len(inputItems) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(inputItems[index].Get("role").String()), "assistant") {
			return index
		}
	}
	for index, item := range inputItems {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		if role != "developer" && role != "system" {
			return index
		}
	}
	return len(inputItems)
}

func xaiReplayLastAssistantMessage(items []gjson.Result) (gjson.Result, bool) {
	for i := len(items) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(items[i].Get("role").String()), "assistant") {
			return items[i], true
		}
	}
	return gjson.Result{}, false
}

func xaiReplayAssistantMessage(items [][]byte) (gjson.Result, bool) {
	for _, raw := range items {
		item := gjson.ParseBytes(raw)
		if strings.TrimSpace(item.Get("type").String()) == "message" && strings.EqualFold(strings.TrimSpace(item.Get("role").String()), "assistant") {
			return item, true
		}
	}
	return gjson.Result{}, false
}

type xaiReplayMessagePart struct{ typ, value string }

func xaiReplayAssistantContentEqual(left, right gjson.Result) bool {
	leftParts, leftOK := xaiReplayMessageParts(left)
	rightParts, rightOK := xaiReplayMessageParts(right)
	if !leftOK || !rightOK || len(leftParts) != len(rightParts) {
		return false
	}
	for i := range leftParts {
		if leftParts[i] != rightParts[i] {
			return false
		}
	}
	return true
}

func xaiReplayMessageParts(content gjson.Result) ([]xaiReplayMessagePart, bool) {
	if content.Type == gjson.String {
		return []xaiReplayMessagePart{{typ: "output_text", value: content.String()}}, true
	}
	if !content.IsArray() {
		return nil, false
	}
	parts := make([]xaiReplayMessagePart, 0, len(content.Array()))
	for _, part := range content.Array() {
		typ := strings.TrimSpace(part.Get("type").String())
		switch typ {
		case "output_text":
			if part.Get("text").Type != gjson.String {
				return nil, false
			}
			parts = append(parts, xaiReplayMessagePart{typ: typ, value: part.Get("text").String()})
		case "refusal":
			if part.Get("refusal").Type != gjson.String {
				return nil, false
			}
			parts = append(parts, xaiReplayMessagePart{typ: typ, value: part.Get("refusal").String()})
		default:
			return nil, false
		}
	}
	return parts, len(parts) > 0
}

func xaiReplayToolCallKey(item gjson.Result) string {
	typ := strings.TrimSpace(item.Get("type").String())
	if typ != "function_call" && typ != "custom_tool_call" {
		return ""
	}
	id := strings.TrimSpace(item.Get("call_id").String())
	if id == "" {
		return ""
	}
	return typ + ":" + id
}

func cacheXAIReasoningReplayFromCompleted(ctx context.Context, scope xaiReasoningReplayScope, completedData []byte) {
	if !scope.valid() {
		return
	}
	output := gjson.GetBytes(completedData, "response.output")
	if !output.IsArray() {
		return
	}
	items := make([][]byte, 0, len(output.Array()))
	for _, item := range output.Array() {
		switch strings.TrimSpace(item.Get("type").String()) {
		case "reasoning", "message", "function_call", "custom_tool_call":
			items = append(items, []byte(item.Raw))
		}
	}
	switch internalcache.StoreXAIReasoningReplayItems(ctx, scope.modelName, scope.sessionKey, items) {
	case internalcache.XAIReasoningReplayNoReplayableState:
		_ = internalcache.DeleteXAIReasoningReplayItemRequired(ctx, scope.modelName, scope.sessionKey)
	case internalcache.XAIReasoningReplayStoreBackendError:
		log.Debug("xai reasoning replay cache store failed; retaining previous state")
	}
}

func clearXAIReasoningReplayAfterCompaction(ctx context.Context, scope xaiReasoningReplayScope) {
	if scope.valid() {
		_ = internalcache.DeleteXAIReasoningReplayItemRequired(ctx, scope.modelName, scope.sessionKey)
	}
}

func clearXAIReasoningReplayOnInvalidSignature(ctx context.Context, scope xaiReasoningReplayScope, statusCode int, body []byte) error {
	if !scope.valid() {
		return nil
	}
	lower := strings.ToLower(string(body))
	if statusCode == 400 && (strings.Contains(lower, "invalid_encrypted_content") || strings.Contains(lower, "invalid signature") || strings.Contains(lower, "thinking_signature_invalid")) {
		return internalcache.DeleteXAIReasoningReplayItemRequired(ctx, scope.modelName, scope.sessionKey)
	}
	return nil
}
