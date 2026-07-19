package cache

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestXAIReasoningReplayCacheStoresNormalizedBatch(t *testing.T) {
	ClearXAIReasoningReplayCache()
	t.Cleanup(ClearXAIReasoningReplayCache)
	encrypted := testXAIEncryptedContent(1)
	items := [][]byte{
		[]byte(`{"type":"reasoning","summary":[{"type":"summary_text","text":"visible"}],"encrypted_content":"` + encrypted + `"}`),
		[]byte(`{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}`),
	}
	if !CacheXAIReasoningReplayItems("grok-4.3", "session-1", items) {
		t.Fatal("expected replay batch to be cached")
	}
	got, ok := GetXAIReasoningReplayItems("grok-4.3", "session-1")
	if !ok || len(got) != 2 {
		t.Fatalf("cached items = %d, %v, want 2, true", len(got), ok)
	}
	if gjson.GetBytes(got[0], "encrypted_content").String() != encrypted || len(gjson.GetBytes(got[0], "summary").Array()) != 0 {
		t.Fatalf("reasoning item was not normalized: %s", got[0])
	}
	if gjson.GetBytes(got[1], "status").Exists() || gjson.GetBytes(got[1], "content.0.text").String() != "answer" {
		t.Fatalf("assistant item was not normalized: %s", got[1])
	}
}

func TestXAIReasoningReplayCacheRejectsForeignSignature(t *testing.T) {
	ClearXAIReasoningReplayCache()
	t.Cleanup(ClearXAIReasoningReplayCache)
	if CacheXAIReasoningReplayItem("grok-4.3", "session-1", []byte(`{"type":"reasoning","encrypted_content":"gAAAAABforeign"}`)) {
		t.Fatal("expected foreign encrypted_content to be rejected")
	}
}

func TestXAIReasoningReplayCacheEvictsOldestEntries(t *testing.T) {
	ClearXAIReasoningReplayCache()
	t.Cleanup(ClearXAIReasoningReplayCache)
	now := time.Now()
	xaiReasoningReplayMu.Lock()
	for i := 0; i < 3; i++ {
		key := xaiReasoningReplayCacheKey("grok-4.3", string(rune('a'+i)))
		xaiReasoningReplayEntries[key] = xaiReasoningReplayEntry{Items: [][]byte{[]byte(`{"type":"function_call","call_id":"call","name":"tool","arguments":"{}"}`)}, Timestamp: now.Add(time.Duration(i) * time.Second)}
	}
	evictOldestXAIReasoningReplayEntriesLocked(2)
	remaining := len(xaiReasoningReplayEntries)
	xaiReasoningReplayMu.Unlock()
	if remaining != 1 {
		t.Fatalf("remaining entries = %d, want 1", remaining)
	}
	if _, ok := GetXAIReasoningReplayItems("grok-4.3", "c"); !ok {
		t.Fatal("newest entry was evicted")
	}
}

func testXAIEncryptedContent(seed byte) string {
	buf := make([]byte, 0, 256)
	for i := 0; len(buf) < 256; i++ {
		sum := sha256.Sum256([]byte{seed, byte(i), byte(i >> 8)})
		buf = append(buf, sum[:]...)
	}
	return base64.RawStdEncoding.EncodeToString(buf[:256])
}
