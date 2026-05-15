package codex

import "testing"

func TestDetermineAutoDisableFreePlanUsesWeeklyWindow(t *testing.T) {
	weeklyAtLimit := 100.0
	weeklyBelowLimit := 99.0

	tests := []struct {
		name    string
		windows []QuotaWindow
		want    *bool
	}{
		{
			name: "missing weekly window returns nil",
			windows: []QuotaWindow{
				{ID: "five-hour"},
			},
			want: nil,
		},
		{
			name: "weekly under limit keeps enabled",
			windows: []QuotaWindow{
				{ID: "weekly", UsedPercent: &weeklyBelowLimit},
			},
			want: boolPtr(false),
		},
		{
			name: "weekly at limit disables",
			windows: []QuotaWindow{
				{ID: "weekly", UsedPercent: &weeklyAtLimit},
			},
			want: boolPtr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineAutoDisable("free", tt.windows)
			assertBoolPtrEqual(t, got, tt.want)
		})
	}
}

func TestDetermineAutoDisableNonFreeRequiresBothWindowsBelowLimitToEnable(t *testing.T) {
	fiveHourBelowLimit := 99.0
	weeklyBelowLimit := 99.0
	fiveHourAtLimit := 100.0
	weeklyAtLimit := 100.0

	tests := []struct {
		name    string
		windows []QuotaWindow
		want    *bool
	}{
		{
			name: "missing five-hour window returns nil",
			windows: []QuotaWindow{
				{ID: "weekly", UsedPercent: &weeklyBelowLimit},
			},
			want: nil,
		},
		{
			name: "missing weekly window returns nil",
			windows: []QuotaWindow{
				{ID: "five-hour", UsedPercent: &fiveHourBelowLimit},
			},
			want: nil,
		},
		{
			name: "both windows below limit keeps enabled",
			windows: []QuotaWindow{
				{ID: "five-hour", UsedPercent: &fiveHourBelowLimit},
				{ID: "weekly", UsedPercent: &weeklyBelowLimit},
			},
			want: boolPtr(false),
		},
		{
			name: "five-hour at limit disables",
			windows: []QuotaWindow{
				{ID: "five-hour", UsedPercent: &fiveHourAtLimit},
				{ID: "weekly", UsedPercent: &weeklyBelowLimit},
			},
			want: boolPtr(true),
		},
		{
			name: "weekly at limit disables",
			windows: []QuotaWindow{
				{ID: "five-hour", UsedPercent: &fiveHourBelowLimit},
				{ID: "weekly", UsedPercent: &weeklyAtLimit},
			},
			want: boolPtr(true),
		},
		{
			name: "both windows at limit disables",
			windows: []QuotaWindow{
				{ID: "five-hour", UsedPercent: &fiveHourAtLimit},
				{ID: "weekly", UsedPercent: &weeklyAtLimit},
			},
			want: boolPtr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineAutoDisable("plus", tt.windows)
			assertBoolPtrEqual(t, got, tt.want)
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func assertBoolPtrEqual(t *testing.T, got, want *bool) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("expected nil, got %v", *got)
		}
		return
	}
	if got == nil {
		t.Fatal("expected non-nil bool pointer")
	}
	if *got != *want {
		t.Fatalf("expected %v, got %v", *want, *got)
	}
}
