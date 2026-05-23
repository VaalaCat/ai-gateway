package channel

import (
	"sort"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
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
