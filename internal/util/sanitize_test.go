package util

import (
	"testing"
)

func TestSanitizeFunctionName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Normal", "valid_name", "valid_name"},
		{"With Dots", "name.with.dots", "name.with.dots"},
		{"With Colons", "name:with:colons", "name:with:colons"},
		{"With Dashes", "name-with-dashes", "name-with-dashes"},
		{"Mixed Allowed", "name.with_dots:colons-dashes", "name.with_dots:colons-dashes"},
		{"Invalid Characters", "name!with@invalid#chars", "name_with_invalid_chars"},
		{"Spaces", "name with spaces", "name_with_spaces"},
		{"Non-ASCII", "name_with_你好_chars", "name_with____chars"},
		{"Starts with digit", "123name", "_123name"},
		{"Starts with dot", ".name", "_.name"},
		{"Starts with colon", ":name", "_:name"},
		{"Starts with dash", "-name", "_-name"},
		{"Starts with invalid char", "!name", "_name"},
		{"Exactly 64 chars", "this_is_a_very_long_name_that_exactly_reaches_sixty_four_charact", "this_is_a_very_long_name_that_exactly_reaches_sixty_four_charact"},
		{"Too long (65 chars)", "this_is_a_very_long_name_that_exactly_reaches_sixty_four_charactX", "this_is_a_very_long_name_that_exactly_reaches_sixty_four_charact"},
		{"Very long", "this_is_a_very_long_name_that_exceeds_the_sixty_four_character_limit_for_function_names", "this_is_a_very_long_name_that_exceeds_the_sixty_four_character_l"},
		{"Starts with digit (64 chars total)", "1234567890123456789012345678901234567890123456789012345678901234", "_123456789012345678901234567890123456789012345678901234567890123"},
		{"Starts with invalid char (64 chars total)", "!234567890123456789012345678901234567890123456789012345678901234", "_234567890123456789012345678901234567890123456789012345678901234"},
		{"Empty", "", ""},
		{"Single character invalid", "@", "_"},
		{"Single character valid", "a", "a"},
		{"Single character digit", "1", "_1"},
		{"Single character underscore", "_", "_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFunctionName(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFunctionName(%q) = %v, want %v", tt.input, got, tt.expected)
			}
			// Verify Gemini compliance
			if len(got) > 64 {
				t.Errorf("SanitizeFunctionName(%q) result too long: %d", tt.input, len(got))
			}
			if len(got) > 0 {
				first := got[0]
				if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
					t.Errorf("SanitizeFunctionName(%q) result starts with invalid char: %c", tt.input, first)
				}
			}
		})
	}
}

func TestSanitizedToolNameMap(t *testing.T) {
	t.Run("returns map for tools needing sanitization", func(t *testing.T) {
		raw := []byte(`{"tools":[
			{"name":"valid_tool","input_schema":{}},
			{"name":"mcp/server/read","input_schema":{}},
			{"name":"tool@v2","input_schema":{}}
		]}`)
		m := SanitizedToolNameMap(raw)
		if m == nil {
			t.Fatal("expected non-nil map")
		}
		if m["mcp_server_read"] != "mcp/server/read" {
			t.Errorf("expected mcp_server_read → mcp/server/read, got %q", m["mcp_server_read"])
		}
		if m["tool_v2"] != "tool@v2" {
			t.Errorf("expected tool_v2 → tool@v2, got %q", m["tool_v2"])
		}
		if _, exists := m["valid_tool"]; exists {
			t.Error("valid_tool should not be in the map (no sanitization needed)")
		}
	})

	t.Run("returns nil when no tools need sanitization", func(t *testing.T) {
		raw := []byte(`{"tools":[{"name":"Read","input_schema":{}},{"name":"Write","input_schema":{}}]}`)
		m := SanitizedToolNameMap(raw)
		if m != nil {
			t.Errorf("expected nil, got %v", m)
		}
	})

	t.Run("returns nil for empty/missing tools", func(t *testing.T) {
		if m := SanitizedToolNameMap([]byte(`{}`)); m != nil {
			t.Error("expected nil for no tools")
		}
		if m := SanitizedToolNameMap(nil); m != nil {
			t.Error("expected nil for nil input")
		}
	})

	t.Run("collision keeps first mapping", func(t *testing.T) {
		raw := []byte(`{"tools":[
			{"name":"read/file","input_schema":{}},
			{"name":"read@file","input_schema":{}}
		]}`)
		m := SanitizedToolNameMap(raw)
		if m == nil {
			t.Fatal("expected non-nil map")
		}
		if m["read_file"] != "read/file" {
			t.Errorf("expected first mapping read/file, got %q", m["read_file"])
		}
	})
}

func TestRestoreSanitizedToolName(t *testing.T) {
	m := map[string]string{
		"mcp_server_read": "mcp/server/read",
		"tool_v2":         "tool@v2",
	}

	if got := RestoreSanitizedToolName(m, "mcp_server_read"); got != "mcp/server/read" {
		t.Errorf("expected mcp/server/read, got %q", got)
	}
	if got := RestoreSanitizedToolName(m, "unknown"); got != "unknown" {
		t.Errorf("expected passthrough for unknown, got %q", got)
	}
	if got := RestoreSanitizedToolName(nil, "name"); got != "name" {
		t.Errorf("expected passthrough for nil map, got %q", got)
	}
	if got := RestoreSanitizedToolName(m, ""); got != "" {
		t.Errorf("expected empty for empty name, got %q", got)
	}
}

func TestSanitizedFunctionNameMapDisambiguatesCollisionsDeterministically(t *testing.T) {
	first := "mcp__plugin_cloudflare_cloudflare-builds__workers_builds_get_build"
	second := "mcp__plugin_cloudflare_cloudflare-builds__workers_builds_get_build_logs"
	raw := []byte(`{"tools":[{"name":"` + second + `"},{"name":"` + first + `"},{"name":"` + second + `"}]}`)

	nameMap := SanitizedFunctionNameMap(raw)
	if nameMap[first] == nameMap[second] {
		t.Fatalf("colliding names map to %q", nameMap[first])
	}
	if len(nameMap[first]) > 64 || len(nameMap[second]) > 64 {
		t.Fatalf("mapped names exceed 64 bytes: %q, %q", nameMap[first], nameMap[second])
	}
	if repeated := SanitizedFunctionNameMap(raw); repeated[first] != nameMap[first] || repeated[second] != nameMap[second] {
		t.Fatalf("mapping is not deterministic: %v then %v", nameMap, repeated)
	}
	reverse := DisambiguatedToolNameMap(raw)
	if reverse[nameMap[first]] != first || reverse[nameMap[second]] != second {
		t.Fatalf("reverse mapping does not restore originals: %v", reverse)
	}
}

func TestDeduplicateFunctionDeclarationsPreservesFirstDeclaration(t *testing.T) {
	raw := []byte(`[{"name":"lookup","description":"first"},{"name":"lookup","description":"second"},{"name":"other"},{"description":"unnamed"}]`)
	got := string(DeduplicateFunctionDeclarations(raw))
	want := `[{"name":"lookup","description":"first"},{"name":"other"},{"description":"unnamed"}]`
	if got != want {
		t.Fatalf("DeduplicateFunctionDeclarations() = %s, want %s", got, want)
	}
}
