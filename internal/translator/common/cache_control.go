package common

import (
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// AttachCacheControl copies a Claude-compatible cache_control object from src onto dst.
func AttachCacheControl(dst []byte, src gjson.Result) []byte {
	cacheControl := src.Get("cache_control")
	if !cacheControl.Exists() || cacheControl.Type == gjson.Null || !cacheControl.IsObject() {
		return dst
	}
	out, err := sjson.SetRawBytes(dst, "cache_control", []byte(cacheControl.Raw))
	if err != nil {
		return dst
	}
	return out
}

// AttachMessageCacheControl applies message-level cache_control to the last content block.
// An existing part-level marker takes precedence.
func AttachMessageCacheControl(msg []byte, src gjson.Result) []byte {
	cacheControl := src.Get("cache_control")
	if !cacheControl.Exists() || cacheControl.Type == gjson.Null || !cacheControl.IsObject() {
		return msg
	}

	content := gjson.GetBytes(msg, "content")
	if content.IsArray() {
		parts := content.Array()
		if len(parts) == 0 || parts[len(parts)-1].Get("cache_control").Exists() {
			return msg
		}
		path := fmt.Sprintf("content.%d.cache_control", len(parts)-1)
		out, err := sjson.SetRawBytes(msg, path, []byte(cacheControl.Raw))
		if err != nil {
			return msg
		}
		return out
	}

	if content.Type != gjson.String {
		return msg
	}
	textPart := []byte(`{"type":"text","text":""}`)
	textPart, _ = sjson.SetBytes(textPart, "text", content.String())
	textPart, err := sjson.SetRawBytes(textPart, "cache_control", []byte(cacheControl.Raw))
	if err != nil {
		return msg
	}
	out, err := sjson.SetRawBytes(msg, "content", []byte("[]"))
	if err != nil {
		return msg
	}
	out, _ = sjson.SetRawBytes(out, "content.-1", textPart)
	return out
}
