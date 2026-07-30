package helps

import (
	"context"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/signature"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SanitizeOpenAIResponsesReasoningEncryptedContent removes reasoning fields
// that an OpenAI Responses upstream cannot safely replay.
func SanitizeOpenAIResponsesReasoningEncryptedContent(ctx context.Context, provider string, body []byte) []byte {
	inputResult := gjson.GetBytes(body, "input")
	if !inputResult.Exists() || !inputResult.IsArray() {
		return body
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai responses upstream"
	}

	// A reasoning item with an id but no usable encrypted_content is treated
	// as a persisted-item lookup. Requests with store disabled cannot resolve
	// that lookup, so remove the orphan id unless store is explicitly enabled.
	stripOrphanReasoningIDs := !gjson.GetBytes(body, "store").Bool()
	items := inputResult.Array()

	var rebuilt []byte
	itemsWritten := 0
	keep := func(raw string) {
		if rebuilt == nil {
			return
		}
		if itemsWritten > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, raw...)
		itemsWritten++
	}
	startRebuild := func(index int) {
		if rebuilt != nil {
			return
		}
		rebuilt = make([]byte, 0, len(inputResult.Raw))
		rebuilt = append(rebuilt, '[')
		for i := 0; i < index; i++ {
			keep(items[i].Raw)
		}
	}

	for index, item := range items {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			keep(item.Raw)
			continue
		}

		encryptedContent := item.Get("encrypted_content")
		itemID := strings.TrimSpace(item.Get("id").String())
		if itemID == "" {
			itemID = fmt.Sprintf("input[%d]", index)
		}

		if !encryptedContent.Exists() {
			if stripOrphanReasoningIDs && item.Get("id").Exists() {
				nextItem, errDelete := sjson.Delete(item.Raw, "id")
				if errDelete != nil {
					LogWithRequestID(ctx).Debugf("%s: failed to drop orphan reasoning id at input[%d]: %v", provider, index, errDelete)
					keep(item.Raw)
					continue
				}
				startRebuild(index)
				keep(nextItem)
				LogWithRequestID(ctx).Debugf("%s: dropped orphan reasoning id at input[%d] item_id=%q reason=missing encrypted_content with store disabled", provider, index, itemID)
				continue
			}
			keep(item.Raw)
			continue
		}

		reason := ""
		switch encryptedContent.Type {
		case gjson.String:
			rawSignature := encryptedContent.String()
			if rawSignature != strings.TrimSpace(rawSignature) {
				reason = "encrypted_content has leading or trailing whitespace"
			} else if _, errInspect := signature.InspectGPTReasoningSignature(rawSignature); errInspect != nil {
				reason = errInspect.Error()
			}
		case gjson.Null:
			reason = "encrypted_content is null"
		default:
			reason = fmt.Sprintf("encrypted_content must be a string, got %s", encryptedContent.Type.String())
		}
		if reason == "" {
			keep(item.Raw)
			continue
		}

		nextItem, errDelete := sjson.Delete(item.Raw, "encrypted_content")
		if errDelete != nil {
			LogWithRequestID(ctx).Debugf("%s: failed to drop invalid reasoning encrypted_content at input[%d]: %v", provider, index, errDelete)
			keep(item.Raw)
			continue
		}
		if stripOrphanReasoningIDs && item.Get("id").Exists() {
			if nextWithoutID, errDeleteID := sjson.Delete(nextItem, "id"); errDeleteID != nil {
				LogWithRequestID(ctx).Debugf("%s: failed to drop reasoning id after invalid encrypted_content at input[%d]: %v", provider, index, errDeleteID)
			} else {
				nextItem = nextWithoutID
			}
		}

		startRebuild(index)
		keep(nextItem)
		LogWithRequestID(ctx).Debugf("%s: dropped invalid reasoning encrypted_content at input[%d] item_id=%q reason=%s", provider, index, itemID, reason)
	}

	if rebuilt == nil {
		return body
	}
	rebuilt = append(rebuilt, ']')

	updated, errSet := sjson.SetRawBytes(body, "input", rebuilt)
	if errSet != nil {
		LogWithRequestID(ctx).Debugf("%s: failed to rebuild input array while sanitizing reasoning encrypted_content: %v", provider, errSet)
		return body
	}
	return updated
}
