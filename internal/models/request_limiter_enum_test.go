package models

import "testing"

func TestValidAction(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{name: "success: reject", action: LimiterActionReject, want: true},
		{name: "success: wait", action: LimiterActionWait, want: true},
		{name: "failure: unknown", action: "drop", want: false},
		{name: "boundary: empty", action: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidAction(tc.action); got != tc.want {
				t.Fatalf("ValidAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

func TestValidChannelScope(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		want  bool
	}{
		{name: "success: admin", scope: LimiterScopeAdmin, want: true},
		{name: "success: private", scope: LimiterScopePrivate, want: true},
		{name: "success: all", scope: LimiterScopeAll, want: true},
		// 边界：空串向后兼容视为 admin，放行
		{name: "boundary: empty defaults to admin", scope: "", want: true},
		{name: "failure: unknown", scope: "tenant", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidChannelScope(tc.scope); got != tc.want {
				t.Fatalf("ValidChannelScope(%q) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}
}
