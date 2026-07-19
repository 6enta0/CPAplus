package cache

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/signature"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	XAIReasoningReplayCacheTTL            = time.Hour
	XAIReasoningReplayCacheMaxEntries     = 10240
	XAIReasoningReplayCacheEvictBatchSize = 128
)

type xaiReasoningReplayEntry struct {
	Items     [][]byte
	Timestamp time.Time
}

var (
	xaiReasoningReplayMu      sync.Mutex
	xaiReasoningReplayEntries = make(map[string]xaiReasoningReplayEntry)
)

type XAIReasoningReplayStoreStatus int

const (
	XAIReasoningReplayStoreInvalidArgs XAIReasoningReplayStoreStatus = iota
	XAIReasoningReplayStored
	XAIReasoningReplayNoReplayableState
	XAIReasoningReplayStoreBackendError
)

func CacheXAIReasoningReplayItem(modelName, sessionKey string, item []byte) bool {
	return CacheXAIReasoningReplayItems(modelName, sessionKey, [][]byte{item})
}

func CacheXAIReasoningReplayItems(modelName, sessionKey string, items [][]byte) bool {
	return StoreXAIReasoningReplayItems(context.Background(), modelName, sessionKey, items) == XAIReasoningReplayStored
}

func CacheXAIReasoningReplayItemsBestEffort(ctx context.Context, modelName, sessionKey string, items [][]byte) bool {
	return StoreXAIReasoningReplayItems(ctx, modelName, sessionKey, items) == XAIReasoningReplayStored
}

func StoreXAIReasoningReplayItems(_ context.Context, modelName, sessionKey string, items [][]byte) XAIReasoningReplayStoreStatus {
	key := xaiReasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return XAIReasoningReplayStoreInvalidArgs
	}
	normalized, ok := normalizeXAIReasoningReplayItems(items)
	if !ok {
		return XAIReasoningReplayNoReplayableState
	}
	cacheCleanupOnce.Do(startCacheCleanup)
	xaiReasoningReplayMu.Lock()
	defer xaiReasoningReplayMu.Unlock()
	xaiReasoningReplayEntries[key] = xaiReasoningReplayEntry{Items: normalized, Timestamp: time.Now()}
	if len(xaiReasoningReplayEntries) > XAIReasoningReplayCacheMaxEntries {
		evictOldestXAIReasoningReplayEntriesLocked(XAIReasoningReplayCacheEvictBatchSize)
	}
	return XAIReasoningReplayStored
}

func GetXAIReasoningReplayItem(modelName, sessionKey string) ([]byte, bool) {
	items, ok := GetXAIReasoningReplayItems(modelName, sessionKey)
	if !ok || len(items) == 0 {
		return nil, false
	}
	return items[0], true
}

func GetXAIReasoningReplayItems(modelName, sessionKey string) ([][]byte, bool) {
	items, ok, err := GetXAIReasoningReplayItemsRequired(context.Background(), modelName, sessionKey)
	if err != nil {
		return nil, false
	}
	return items, ok
}

func GetXAIReasoningReplayItemsRequired(_ context.Context, modelName, sessionKey string) ([][]byte, bool, error) {
	key := xaiReasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return nil, false, nil
	}
	now := time.Now()
	xaiReasoningReplayMu.Lock()
	defer xaiReasoningReplayMu.Unlock()
	entry, ok := xaiReasoningReplayEntries[key]
	if !ok {
		return nil, false, nil
	}
	if now.Sub(entry.Timestamp) > XAIReasoningReplayCacheTTL {
		delete(xaiReasoningReplayEntries, key)
		return nil, false, nil
	}
	entry.Timestamp = now
	xaiReasoningReplayEntries[key] = entry
	return cloneXAIReasoningReplayItems(entry.Items), true, nil
}

func DeleteXAIReasoningReplayItem(modelName, sessionKey string) {
	_ = DeleteXAIReasoningReplayItemRequired(context.Background(), modelName, sessionKey)
}

func DeleteXAIReasoningReplayItemRequired(_ context.Context, modelName, sessionKey string) error {
	key := xaiReasoningReplayCacheKey(modelName, sessionKey)
	if key == "" {
		return nil
	}
	xaiReasoningReplayMu.Lock()
	delete(xaiReasoningReplayEntries, key)
	xaiReasoningReplayMu.Unlock()
	return nil
}

func ClearXAIReasoningReplayCache() {
	xaiReasoningReplayMu.Lock()
	xaiReasoningReplayEntries = make(map[string]xaiReasoningReplayEntry)
	xaiReasoningReplayMu.Unlock()
}

func xaiReasoningReplayCacheKey(modelName, sessionKey string) string {
	modelName = strings.TrimSpace(modelName)
	sessionKey = strings.TrimSpace(sessionKey)
	if modelName == "" || sessionKey == "" {
		return ""
	}
	return strings.Join([]string{"xai-reasoning-replay", modelName, sessionKey}, "\x00")
}

func normalizeXAIReasoningReplayItems(items [][]byte) ([][]byte, bool) {
	normalized := make([][]byte, 0, len(items))
	hasAnchor := false
	for _, item := range items {
		normalizedItem, ok := normalizeXAIReasoningReplayItem(item)
		if !ok {
			continue
		}
		normalized = append(normalized, normalizedItem)
		switch gjson.GetBytes(normalizedItem, "type").String() {
		case "reasoning", "function_call", "custom_tool_call":
			hasAnchor = true
		}
	}
	return normalized, hasAnchor
}

func normalizeXAIReasoningReplayItem(item []byte) ([]byte, bool) {
	root := gjson.ParseBytes(item)
	switch strings.TrimSpace(root.Get("type").String()) {
	case "reasoning":
		encrypted := root.Get("encrypted_content")
		if encrypted.Type != gjson.String || encrypted.String() == "" {
			return nil, false
		}
		if _, err := signature.InspectGrokEncryptedContent(encrypted.String()); err != nil {
			return nil, false
		}
		out := []byte(`{"type":"reasoning","summary":[],"content":null}`)
		out, _ = sjson.SetBytes(out, "encrypted_content", encrypted.String())
		return out, true
	case "message":
		if !strings.EqualFold(strings.TrimSpace(root.Get("role").String()), "assistant") || !root.Get("content").IsArray() {
			return nil, false
		}
		out := []byte(`{"type":"message","role":"assistant","content":[]}`)
		for _, part := range root.Get("content").Array() {
			var next []byte
			switch strings.TrimSpace(part.Get("type").String()) {
			case "output_text":
				if part.Get("text").Type != gjson.String {
					continue
				}
				next = []byte(`{"type":"output_text","text":""}`)
				next, _ = sjson.SetBytes(next, "text", part.Get("text").String())
			case "refusal":
				if part.Get("refusal").Type != gjson.String {
					continue
				}
				next = []byte(`{"type":"refusal","refusal":""}`)
				next, _ = sjson.SetBytes(next, "refusal", part.Get("refusal").String())
			default:
				continue
			}
			out, _ = sjson.SetRawBytes(out, "content.-1", next)
		}
		if len(gjson.GetBytes(out, "content").Array()) == 0 {
			return nil, false
		}
		return out, true
	case "function_call":
		callID, name, args := strings.TrimSpace(root.Get("call_id").String()), strings.TrimSpace(root.Get("name").String()), root.Get("arguments")
		if callID == "" || name == "" || args.Type != gjson.String {
			return nil, false
		}
		out := []byte(`{"type":"function_call"}`)
		out, _ = sjson.SetBytes(out, "call_id", callID)
		out, _ = sjson.SetBytes(out, "name", name)
		out, _ = sjson.SetBytes(out, "arguments", args.String())
		return out, true
	case "custom_tool_call":
		callID, name, input := strings.TrimSpace(root.Get("call_id").String()), strings.TrimSpace(root.Get("name").String()), root.Get("input")
		if callID == "" || name == "" || !input.Exists() {
			return nil, false
		}
		out := []byte(`{"type":"custom_tool_call","status":"completed"}`)
		out, _ = sjson.SetBytes(out, "call_id", callID)
		out, _ = sjson.SetBytes(out, "name", name)
		if input.Type == gjson.String {
			out, _ = sjson.SetBytes(out, "input", input.String())
		} else {
			out, _ = sjson.SetRawBytes(out, "input", []byte(input.Raw))
		}
		return out, true
	default:
		return nil, false
	}
}

func cloneXAIReasoningReplayItems(items [][]byte) [][]byte {
	cloned := make([][]byte, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, append([]byte(nil), item...))
	}
	return cloned
}

func evictOldestXAIReasoningReplayEntriesLocked(count int) {
	type candidate struct {
		key       string
		timestamp time.Time
	}
	candidates := make([]candidate, 0, len(xaiReasoningReplayEntries))
	for key, entry := range xaiReasoningReplayEntries {
		candidates = append(candidates, candidate{key, entry.Timestamp})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].timestamp.Before(candidates[j].timestamp) })
	if count > len(candidates) {
		count = len(candidates)
	}
	for i := 0; i < count; i++ {
		delete(xaiReasoningReplayEntries, candidates[i].key)
	}
}

func purgeExpiredXAIReasoningReplayCache(now time.Time) {
	xaiReasoningReplayMu.Lock()
	for key, entry := range xaiReasoningReplayEntries {
		if now.Sub(entry.Timestamp) > XAIReasoningReplayCacheTTL {
			delete(xaiReasoningReplayEntries, key)
		}
	}
	xaiReasoningReplayMu.Unlock()
}
