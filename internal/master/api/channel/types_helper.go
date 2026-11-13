package channel

import (
	"errors"
	"net/http"
	"sort"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ListProviderTypes returns all channel type IDs that represent real, user-selectable
// providers: every key in newAPIConstant.ChannelTypeNames except the sentinel
// ChannelTypeUnknown (0) and ChannelTypeDummy (terminal count marker). The result is
// sorted in ascending order so callers get a stable UI ordering.
func ListProviderTypes() []int {
	ids := make([]int, 0, len(newAPIConstant.ChannelTypeNames))
	for id := range newAPIConstant.ChannelTypeNames {
		if id == newAPIConstant.ChannelTypeUnknown || id == newAPIConstant.ChannelTypeDummy {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// channelInputAPIError 将认证校验错误转成带错误码的输入错误。
func channelInputAPIError(err error) error {
	// 1. 返回认证错误的固定文本和错误码。
	for _, authErr := range []error{models.ErrUpstreamAuthHeader, models.ErrUpstreamAuthValue, models.ErrUpstreamAuthProtocol} {
		if errors.Is(err, authErr) {
			return api.ErrorWithCode(http.StatusBadRequest, "invalid_upstream_auth", authErr.Error(), nil)
		}
	}
	// 2. 其余输入错误沿用普通响应。
	return api.BadRequestError(err.Error(), err)
}
