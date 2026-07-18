package gemini

import "testing"

func TestParseInteractionsRequestTargetRequiresExactlyOneTarget(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing", raw: `{}`, want: "request requires exactly one of model or agent"},
		{name: "both", raw: `{"model":"gemini-test","agent":"agent-test"}`, want: "request requires exactly one of model or agent"},
		{name: "bad stream", raw: `{"model":"gemini-test","stream":"true"}`, want: "stream must be a boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseInteractionsRequestTarget([]byte(tt.raw)); err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPrepareInteractionsExecutionNormalizesModelResource(t *testing.T) {
	target, err := ParseInteractionsRequestTarget([]byte(`{"model":"models/gemini-test","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	model, body, metadata := prepareInteractionsExecution([]byte(`{"model":"models/gemini-test","stream":true}`), target)
	if model != "gemini-test" || string(body) != `{"model":"gemini-test","stream":true}` {
		t.Fatalf("normalized target = %q, body = %s", model, body)
	}
	if metadata.EntryProtocol != "interactions" || metadata.ExitProtocol != "interactions" || !metadata.Stream {
		t.Fatalf("unexpected execution metadata: %+v", metadata)
	}
}

func TestPrepareInteractionsExecutionUsesAgentAuthSelectionModel(t *testing.T) {
	target, err := ParseInteractionsRequestTarget([]byte(`{"agent":"agents/researcher"}`))
	if err != nil {
		t.Fatal(err)
	}
	model, body, metadata := prepareInteractionsExecution([]byte(`{"agent":"agents/researcher"}`), target)
	if model != interactionsAgentAuthSelectionModel || string(body) != `{"agent":"agents/researcher"}` {
		t.Fatalf("agent target = %q, body = %s", model, body)
	}
	if metadata.Agent != "agents/researcher" || metadata.AuthSelectionModel != interactionsAgentAuthSelectionModel {
		t.Fatalf("unexpected agent metadata: %+v", metadata)
	}
}
