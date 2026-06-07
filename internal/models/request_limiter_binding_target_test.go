package models

import "testing"

func TestValidBindingTarget(t *testing.T) {
	cases := []struct {
		name       string
		keyBy      string
		targetType string
		want       bool
	}{
		// shared: 只能挂全局
		{name: "success: shared+global", keyBy: LimiterKeyShared, targetType: LimiterTargetGlobal, want: true},
		{name: "failure: shared+channel", keyBy: LimiterKeyShared, targetType: LimiterTargetChannel, want: false},
		{name: "failure: shared+user", keyBy: LimiterKeyShared, targetType: LimiterTargetUser, want: false},

		// per_user: global / user_group / user
		{name: "success: per_user+global", keyBy: LimiterKeyPerUser, targetType: LimiterTargetGlobal, want: true},
		{name: "success: per_user+user_group", keyBy: LimiterKeyPerUser, targetType: LimiterTargetUserGroup, want: true},
		{name: "success: per_user+user", keyBy: LimiterKeyPerUser, targetType: LimiterTargetUser, want: true},
		{name: "failure: per_user+channel", keyBy: LimiterKeyPerUser, targetType: LimiterTargetChannel, want: false},

		// per_group: global / user_group
		{name: "success: per_group+global", keyBy: LimiterKeyPerGroup, targetType: LimiterTargetGlobal, want: true},
		{name: "success: per_group+user_group", keyBy: LimiterKeyPerGroup, targetType: LimiterTargetUserGroup, want: true},
		{name: "failure: per_group+user", keyBy: LimiterKeyPerGroup, targetType: LimiterTargetUser, want: false},
		{name: "failure: per_group+channel", keyBy: LimiterKeyPerGroup, targetType: LimiterTargetChannel, want: false},

		// per_channel: global / channel
		{name: "success: per_channel+global", keyBy: LimiterKeyPerChannel, targetType: LimiterTargetGlobal, want: true},
		{name: "success: per_channel+channel", keyBy: LimiterKeyPerChannel, targetType: LimiterTargetChannel, want: true},
		{name: "failure: per_channel+user", keyBy: LimiterKeyPerChannel, targetType: LimiterTargetUser, want: false},

		// per_channel_user: global / channel
		{name: "success: per_channel_user+channel", keyBy: LimiterKeyPerChannelUser, targetType: LimiterTargetChannel, want: true},
		{name: "success: per_channel_user+global", keyBy: LimiterKeyPerChannelUser, targetType: LimiterTargetGlobal, want: true},
		{name: "failure: per_channel_user+user_group", keyBy: LimiterKeyPerChannelUser, targetType: LimiterTargetUserGroup, want: false},

		// 边界：未知 key_by / target_type 一律非法
		{name: "boundary: unknown key_by", keyBy: "per_model", targetType: LimiterTargetGlobal, want: false},
		{name: "boundary: empty key_by", keyBy: "", targetType: LimiterTargetGlobal, want: false},
		{name: "boundary: unknown target_type", keyBy: LimiterKeyPerUser, targetType: "tenant", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidBindingTarget(tc.keyBy, tc.targetType); got != tc.want {
				t.Fatalf("ValidBindingTarget(%q, %q) = %v, want %v", tc.keyBy, tc.targetType, got, tc.want)
			}
		})
	}
}
