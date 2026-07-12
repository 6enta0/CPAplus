package common

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestAttachCacheControlCopiesObject(t *testing.T) {
	src := gjson.Parse(`{"cache_control":{"type":"ephemeral","ttl":"5m"}}`)
	out := AttachCacheControl([]byte(`{"type":"text","text":"hi"}`), src)
	if got := gjson.GetBytes(out, "cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("cache_control.type = %q, want ephemeral", got)
	}
	if got := gjson.GetBytes(out, "cache_control.ttl").String(); got != "5m" {
		t.Fatalf("cache_control.ttl = %q, want 5m", got)
	}
}

func TestAttachMessageCacheControlPromotesStringContent(t *testing.T) {
	src := gjson.Parse(`{"cache_control":{"type":"ephemeral"}}`)
	out := AttachMessageCacheControl([]byte(`{"role":"user","content":"hi"}`), src)
	if got := gjson.GetBytes(out, "content.0.text").String(); got != "hi" {
		t.Fatalf("content text = %q, want hi", got)
	}
	if got := gjson.GetBytes(out, "content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("cache_control.type = %q, want ephemeral", got)
	}
}

func TestAttachMessageCacheControlPreservesPartLevelMarker(t *testing.T) {
	src := gjson.Parse(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`)
	msg := []byte(`{"content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}`)
	out := AttachMessageCacheControl(msg, src)
	if gjson.GetBytes(out, "content.0.cache_control.ttl").Exists() {
		t.Fatalf("message-level marker replaced part-level marker: %s", out)
	}
}
