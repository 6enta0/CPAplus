package registry

import "testing"

func TestGetXAIModelsAndAliases(t *testing.T) {
	models := GetXAIModels()
	if len(models) == 0 {
		t.Fatal("GetXAIModels() returned no models")
	}
	if got := GetStaticModelDefinitionsByChannel("grok"); len(got) != len(models) {
		t.Fatalf("grok alias count = %d, want %d", len(got), len(models))
	}
	model := LookupStaticModelInfo("grok-4.5")
	if model == nil || model.OwnedBy != "xai" || model.Type != "xai" {
		t.Fatalf("grok-4.5 metadata = %#v", model)
	}
}

func TestDetectChangedProvidersIncludesXAI(t *testing.T) {
	oldCatalog := &staticModelsJSON{XAI: []*ModelInfo{{ID: "grok-old"}}}
	newCatalog := &staticModelsJSON{XAI: []*ModelInfo{{ID: "grok-new"}}}
	changed := detectChangedProviders(oldCatalog, newCatalog)
	if len(changed) != 1 || changed[0] != "xai" {
		t.Fatalf("changed providers = %#v, want [xai]", changed)
	}
}
