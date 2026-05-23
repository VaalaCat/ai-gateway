package channel

import (
	"strings"
	"unicode"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Types(_ *app.Context, _ api.EmptyRequest) ([]TypeMeta, error) {
	ids := ListProviderTypes()

	items := make([]TypeMeta, 0, len(ids))
	for _, id := range ids {
		name := newAPIConstant.GetChannelTypeName(id)
		items = append(items, TypeMeta{
			ID:      id,
			Name:    name,
			I18nKey: channelTypeI18nKey(name),
		})
	}
	return items, nil
}

func channelTypeI18nKey(name string) string {
	var builder strings.Builder
	capitalizeNext := true

	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			capitalizeNext = true
			continue
		}
		if capitalizeNext {
			r = unicode.ToUpper(r)
			capitalizeNext = false
		}
		builder.WriteRune(r)
	}

	key := builder.String()
	if key == "" {
		key = "Unknown"
	}
	return "channelType" + key
}
