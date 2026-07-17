package responses

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func convertResponsesToolToOpenAIChatTools(tool gjson.Result) [][]byte {
	switch strings.TrimSpace(tool.Get("type").String()) {
	case "", "function":
		if toolJSON, ok := convertResponsesFunctionToolToOpenAIChat(tool, ""); ok {
			return [][]byte{toolJSON}
		}
	case "namespace":
		return convertResponsesNamespaceToolToOpenAIChat(tool)
	case "custom":
		if toolJSON, ok := convertResponsesCustomToolToOpenAIChat(tool, ""); ok {
			return [][]byte{toolJSON}
		}
	}
	return nil
}

func convertResponsesCustomToolToOpenAIChat(tool gjson.Result, overrideName string) ([]byte, bool) {
	name := strings.TrimSpace(overrideName)
	if name == "" {
		name = responsesToolName(tool)
	}
	if name == "" {
		return nil, false
	}

	toolJSON := []byte(`{"type":"function","function":{"name":"","description":"","parameters":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}}}`)
	toolJSON, _ = sjson.SetBytes(toolJSON, "function.name", name)
	if description := responsesToolDescription(tool); description != "" {
		toolJSON, _ = sjson.SetBytes(toolJSON, "function.description", description)
	}
	return toolJSON, true
}

func convertResponsesNamespaceToolToOpenAIChat(tool gjson.Result) [][]byte {
	namespaceName := strings.TrimSpace(tool.Get("name").String())
	children := tool.Get("tools")
	if !children.Exists() || !children.IsArray() {
		return nil
	}

	var output [][]byte
	children.ForEach(func(_, child gjson.Result) bool {
		qualifiedName := qualifyResponsesNamespaceToolName(namespaceName, responsesToolName(child))
		switch strings.TrimSpace(child.Get("type").String()) {
		case "", "function":
			if toolJSON, ok := convertResponsesFunctionToolToOpenAIChat(child, qualifiedName); ok {
				output = append(output, toolJSON)
			}
		case "custom":
			if toolJSON, ok := convertResponsesCustomToolToOpenAIChat(child, qualifiedName); ok {
				output = append(output, toolJSON)
			}
		}
		return true
	})
	return output
}

func convertResponsesFunctionToolToOpenAIChat(tool gjson.Result, overrideName string) ([]byte, bool) {
	name := strings.TrimSpace(overrideName)
	if name == "" {
		name = responsesToolName(tool)
	}
	if name == "" {
		return nil, false
	}

	toolJSON := []byte(`{"type":"function","function":{"name":"","description":"","parameters":{}}}`)
	toolJSON, _ = sjson.SetBytes(toolJSON, "function.name", name)
	if description := responsesToolDescription(tool); description != "" {
		toolJSON, _ = sjson.SetBytes(toolJSON, "function.description", description)
	}
	if parameters := responsesToolParameters(tool); parameters.Exists() {
		toolJSON, _ = sjson.SetRawBytes(toolJSON, "function.parameters", []byte(parameters.Raw))
	}
	return toolJSON, true
}

func responsesToolName(tool gjson.Result) string {
	if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
		return name
	}
	return strings.TrimSpace(tool.Get("function.name").String())
}

func responsesToolDescription(tool gjson.Result) string {
	if description := tool.Get("description").String(); description != "" {
		return description
	}
	return tool.Get("function.description").String()
}

func responsesToolParameters(tool gjson.Result) gjson.Result {
	for _, path := range []string{
		"parameters",
		"parametersJsonSchema",
		"input_schema",
		"function.parameters",
		"function.parametersJsonSchema",
	} {
		if parameters := tool.Get(path); parameters.Exists() {
			return parameters
		}
	}
	return gjson.Result{}
}

func responsesToolOutputText(output gjson.Result) string {
	if output.Type == gjson.String {
		return output.String()
	}
	if output.IsArray() {
		var text strings.Builder
		output.ForEach(func(_, part gjson.Result) bool {
			if part.Type == gjson.String {
				text.WriteString(part.String())
				return true
			}
			if value := part.Get("text"); value.Exists() {
				text.WriteString(value.String())
			}
			return true
		})
		return text.String()
	}
	if output.Exists() {
		return output.Raw
	}
	return ""
}

func responsesCustomToolNames(requestRawJSON []byte) map[string]struct{} {
	names := make(map[string]struct{})
	var collect func(gjson.Result, string)
	collect = func(tools gjson.Result, namespaceName string) {
		if !tools.Exists() || !tools.IsArray() {
			return
		}
		tools.ForEach(func(_, tool gjson.Result) bool {
			switch strings.TrimSpace(tool.Get("type").String()) {
			case "custom":
				name := responsesToolName(tool)
				if namespaceName != "" {
					name = qualifyResponsesNamespaceToolName(namespaceName, name)
				}
				if name != "" {
					names[name] = struct{}{}
				}
			case "namespace":
				collect(tool.Get("tools"), strings.TrimSpace(tool.Get("name").String()))
			}
			return true
		})
	}

	root := gjson.ParseBytes(requestRawJSON)
	collect(root.Get("tools"), "")
	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				collect(item.Get("tools"), "")
			}
			return true
		})
	}
	return names
}

func responsesSingleCustomToolName(requestRawJSON []byte) (string, bool) {
	customToolNames := responsesCustomToolNames(requestRawJSON)
	if len(customToolNames) != 1 {
		return "", false
	}

	toolCount := 0
	collect := func(tools gjson.Result) {
		if !tools.Exists() || !tools.IsArray() {
			return
		}
		tools.ForEach(func(_, tool gjson.Result) bool {
			toolCount += len(convertResponsesToolToOpenAIChatTools(tool))
			return true
		})
	}
	root := gjson.ParseBytes(requestRawJSON)
	collect(root.Get("tools"))
	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				collect(item.Get("tools"))
			}
			return true
		})
	}
	for name := range customToolNames {
		return name, toolCount == 1
	}
	return "", false
}

func unwrapCustomToolInput(arguments string) string {
	if input := gjson.Get(arguments, "input"); input.Exists() {
		if input.Type == gjson.String {
			return input.String()
		}
		return input.Raw
	}
	return arguments
}

func qualifyResponsesNamespaceToolName(namespaceName, childName string) string {
	childName = strings.TrimSpace(childName)
	if childName == "" || namespaceName == "" || strings.HasPrefix(childName, "mcp__") {
		return childName
	}
	if strings.HasPrefix(childName, namespaceName) {
		return childName
	}
	if strings.HasSuffix(namespaceName, "__") {
		return namespaceName + childName
	}
	return namespaceName + "__" + childName
}

func splitResponsesQualifiedFunctionCallFromRequest(requestRawJSON []byte, qualifiedName string) (name, namespace string) {
	qualifiedName = strings.TrimSpace(qualifiedName)
	if qualifiedName == "" {
		return "", ""
	}

	var bestNamespace string
	var bestChild string
	collect := func(tools gjson.Result) {
		if !tools.Exists() || !tools.IsArray() {
			return
		}
		tools.ForEach(func(_, tool gjson.Result) bool {
			if strings.TrimSpace(tool.Get("type").String()) != "namespace" {
				return true
			}
			namespaceName := strings.TrimSpace(tool.Get("name").String())
			children := tool.Get("tools")
			if namespaceName == "" || !children.Exists() || !children.IsArray() {
				return true
			}
			children.ForEach(func(_, child gjson.Result) bool {
				childName := responsesToolName(child)
				if childName != "" && qualifyResponsesNamespaceToolName(namespaceName, childName) == qualifiedName {
					bestNamespace = namespaceName
					bestChild = childName
				}
				return true
			})
			return true
		})
	}

	root := gjson.ParseBytes(requestRawJSON)
	collect(root.Get("tools"))
	if input := root.Get("input"); input.Exists() && input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				collect(item.Get("tools"))
			}
			return true
		})
	}
	if bestNamespace == "" || bestChild == "" {
		return qualifiedName, ""
	}
	return bestChild, bestNamespace
}

func pickRequestJSON(originalRequestRawJSON, requestRawJSON []byte) []byte {
	if len(originalRequestRawJSON) > 0 && gjson.ValidBytes(originalRequestRawJSON) {
		return originalRequestRawJSON
	}
	if len(requestRawJSON) > 0 && gjson.ValidBytes(requestRawJSON) {
		return requestRawJSON
	}
	return nil
}

func applyResponsesFunctionCallNamespaceFields(item []byte, requestRawJSON []byte, qualifiedName, itemPath string) []byte {
	name, namespace := splitResponsesQualifiedFunctionCallFromRequest(requestRawJSON, qualifiedName)
	namePath := "name"
	namespacePath := "namespace"
	if itemPath != "" {
		namePath = itemPath + ".name"
		namespacePath = itemPath + ".namespace"
	}
	item, _ = sjson.SetBytes(item, namePath, name)
	if namespace != "" {
		item, _ = sjson.SetBytes(item, namespacePath, namespace)
	} else {
		item, _ = sjson.DeleteBytes(item, namespacePath)
	}
	return item
}
