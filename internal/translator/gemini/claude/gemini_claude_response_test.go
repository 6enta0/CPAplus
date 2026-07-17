package claude

import (
	"context"
	"strings"
	"testing"
)

func TestConvertGeminiResponseToClaudeSignatureOnlyChunkDoesNotOpenTextBlock(t *testing.T) {
	var param any
	first := ConvertGeminiResponseToClaude(context.Background(), "", nil, nil, []byte(`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true}]}}]}`), &param)
	second := ConvertGeminiResponseToClaude(context.Background(), "", nil, nil, []byte(`{"candidates":[{"content":{"parts":[{"thoughtSignature":"signature-only"}]}}]}`), &param)

	combined := ""
	for _, chunk := range append(first, second...) {
		combined += string(chunk)
	}
	if !strings.Contains(combined, `"type":"signature_delta"`) || !strings.Contains(combined, "signature-only") {
		t.Fatalf("signature delta missing: %s", combined)
	}
	if strings.Contains(string(second[0]), `"content_block":{"type":"text"`) {
		t.Fatalf("signature-only chunk opened an empty text block: %s", second[0])
	}
}
