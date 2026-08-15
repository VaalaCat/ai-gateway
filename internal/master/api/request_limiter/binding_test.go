package request_limiter

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestValidateCreateBindingRejectsAPIResourceWithChannelScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		limiter   models.RequestLimiter
		target    string
		targetID  uint
		wantError bool
	}{
		{name: "api service shared", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyShared}, target: models.LimiterTargetAPIService, targetID: 7},
		{name: "api route per user", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyPerUser}, target: models.LimiterTargetAPIRoute, targetID: 9},
		{name: "api upstream per group", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyPerGroup}, target: models.LimiterTargetAPIUpstream, targetID: 11},
		{name: "API scope must be empty", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyShared, ChannelScope: models.LimiterScopeAdmin}, target: models.LimiterTargetAPIService, targetID: 7, wantError: true},
		{name: "API cannot be channel keyed", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyPerChannel}, target: models.LimiterTargetAPIRoute, targetID: 9, wantError: true},
		{name: "API target needs id", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyShared}, target: models.LimiterTargetAPIUpstream, wantError: true},
		{name: "legacy global remains valid", limiter: models.RequestLimiter{KeyBy: models.LimiterKeyShared, ChannelScope: models.LimiterScopeAdmin}, target: models.LimiterTargetGlobal},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotID, err := validateCreateBinding(test.limiter, CreateBindingRequest{TargetType: test.target, TargetID: test.targetID})
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.targetID, gotID)
		})
	}
}
