package model_marketplace

import (
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/dao"
)

type marketplaceDirectoryItem struct {
	kind         MarketplaceModelKind
	name         string
	realIndex    int
	routingIndex int
}

func filterAndPageMarketplaceDirectory(
	directory composedMarketplace,
	req ListRequest,
	page int,
	pageSize int,
) (composedMarketplace, int64) {
	items := make([]marketplaceDirectoryItem, 0, len(directory.real)+len(directory.routing))
	for index, model := range directory.real {
		if matchesRealListRequest(model, req) {
			items = append(items, marketplaceDirectoryItem{
				kind: ModelKindReal, name: model.model.ModelName, realIndex: index,
			})
		}
	}
	for index, model := range directory.routing {
		if matchesRoutingListRequest(model, req) {
			items = append(items, marketplaceDirectoryItem{
				kind: ModelKindRouting, name: model.ModelName, routingIndex: index,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return marketplaceModelOrderLess(
			items[i].name, items[i].kind,
			items[j].name, items[j].kind,
		)
	})

	total := int64(len(items))
	result := emptyComposedMarketplace(directory.viewer)
	if pageSize <= 0 || len(items) == 0 {
		return result, total
	}
	lastPage := (len(items)-1)/pageSize + 1
	if page > lastPage {
		return result, total
	}
	start := (dao.ListOptions{Page: page, PageSize: pageSize}).Offset()
	if start >= len(items) {
		return result, total
	}
	end := min(start+pageSize, len(items))
	for _, item := range items[start:end] {
		switch item.kind {
		case ModelKindReal:
			result.real = append(result.real, directory.real[item.realIndex])
		case ModelKindRouting:
			result.routing = append(result.routing, directory.routing[item.routingIndex])
		}
	}
	return result, total
}

func marketplaceModelOrderLess(
	leftName string,
	leftKind MarketplaceModelKind,
	rightName string,
	rightKind MarketplaceModelKind,
) bool {
	if leftName != rightName {
		return leftName < rightName
	}
	return leftKind < rightKind
}
