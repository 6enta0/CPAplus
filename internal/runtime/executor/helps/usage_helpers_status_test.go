package helps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

type statusCodeErr struct {
	code int
	msg  string
}

func (e statusCodeErr) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "status error"
}

func (e statusCodeErr) StatusCode() int { return e.code }

func TestResolveClientExitOutcome_SuccessDefaultsTo200(t *testing.T) {
	status, msg := resolveClientExitOutcome(context.Background(), false, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if msg != "" {
		t.Fatalf("message = %q, want empty", msg)
	}
}

func TestResolveClientExitOutcome_UsesErrorStatusCodeAndJSONMessage(t *testing.T) {
	err := statusCodeErr{code: http.StatusTooManyRequests, msg: `{"error":{"message":"rate limited"}}`}
	status, msg := resolveClientExitOutcome(context.Background(), true, err)
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	if msg != "rate limited" {
		t.Fatalf("message = %q, want rate limited", msg)
	}
}

func TestResolveClientExitOutcome_UsesContextStatusWhenNoErrorStatus(t *testing.T) {
	ctx := logging.WithResponseStatusHolder(context.Background())
	logging.SetResponseStatus(ctx, http.StatusUnauthorized)
	status, msg := resolveClientExitOutcome(ctx, true, errors.New("unauthorized"))
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if msg != "unauthorized" {
		t.Fatalf("message = %q, want unauthorized", msg)
	}
}

func TestResolveClientExitOutcome_UsesWrappedErrorStatusCode(t *testing.T) {
	err := fmt.Errorf("execute request: %w", statusCodeErr{code: http.StatusServiceUnavailable, msg: "upstream unavailable"})
	status, msg := resolveClientExitOutcome(context.Background(), true, err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if msg != "execute request: upstream unavailable" {
		t.Fatalf("message = %q, want wrapped error message", msg)
	}
}

func TestResolveClientExitOutcome_FailedWithoutStatusRemainsUnknown(t *testing.T) {
	status, msg := resolveClientExitOutcome(context.Background(), true, errors.New("connection closed"))
	if status != 0 {
		t.Fatalf("status = %d, want unknown status 0", status)
	}
	if msg != "connection closed" {
		t.Fatalf("message = %q, want connection closed", msg)
	}
}
func TestResolveClientExitOutcome_ClearsMessageOnSuccess(t *testing.T) {
	status, msg := resolveClientExitOutcome(context.Background(), false, errors.New("should not keep"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if msg != "" {
		t.Fatalf("message = %q, want empty on success", msg)
	}
}

func TestSummarizeUsageError_Truncates(t *testing.T) {
	long := strings.Repeat("a", maxUsageErrorMessageRunes+50)
	got := summarizeUsageError(errors.New(long))
	if got == "" {
		t.Fatal("expected non-empty summary")
	}
	if len([]rune(got)) > maxUsageErrorMessageRunes {
		t.Fatalf("summary length = %d, want <= %d", len([]rune(got)), maxUsageErrorMessageRunes)
	}
}

func TestUsageReporter_BuildRecordCapturesFailureFields(t *testing.T) {
	reporter := NewUsageReporter(context.Background(), "openai-compat", "model-a", nil)
	err := statusCodeErr{code: http.StatusBadRequest, msg: "invalid tools schema"}
	got := reporter.buildRecord(context.Background(), usage.Detail{}, true, err)
	if !got.Failed {
		t.Fatal("expected failed record")
	}
	if got.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got.StatusCode)
	}
	if got.ErrorMessage != "invalid tools schema" {
		t.Fatalf("error message = %q, want invalid tools schema", got.ErrorMessage)
	}
}
