package model_marketplace

import (
	"math"
	"sort"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

// Break caught: paginating the real and routing slices independently would
// return the wrong members for a globally sorted mixed-kind page.
func TestFilterAndPageMarketplaceDirectoryMixedKinds(t *testing.T) {
	viewer := MarketplaceViewer{UserID: 7}
	directory := composedMarketplace{
		viewer: viewer,
		real: []composedRealModel{
			listPageReal("zeta", "OpenAI"),
			listPageReal("alpha", "OpenAI"),
		},
		routing: []RoutingModel{
			listPageRouting("beta"),
			listPageRouting("alpha"),
		},
	}

	got, total := filterAndPageMarketplaceDirectory(directory, ListRequest{}, 2, 2)

	require.Equal(t, int64(4), total)
	require.Equal(t, []string{"routing:beta", "real:zeta"}, listPageNames(got))
	require.Equal(t, viewer, got.viewer)

	firstPage, firstTotal := filterAndPageMarketplaceDirectory(directory, ListRequest{}, 1, 2)
	require.Equal(t, int64(4), firstTotal)
	require.Equal(t, []string{"real:alpha", "routing:alpha"}, listPageNames(firstPage))
}

// Break caught: computing total after slicing or letting provider filters
// retain routing models would produce inconsistent pagination metadata.
func TestFilterAndPageMarketplaceDirectoryProviderBeforePaging(t *testing.T) {
	directory := composedMarketplace{
		real: []composedRealModel{
			listPageReal("zeta", "OpenAI"),
			listPageReal("alpha", "Anthropic"),
			listPageReal("beta", "OpenAI"),
		},
		routing: []RoutingModel{listPageRouting("route")},
	}

	got, total := filterAndPageMarketplaceDirectory(
		directory,
		ListRequest{Provider: " openai "},
		1,
		1,
	)

	require.Equal(t, int64(2), total)
	require.Equal(t, []string{"real:beta"}, listPageNames(got))
}

// Break caught: replacing the established contains search behavior with an
// exact or case-sensitive match would hide valid directory entries.
func TestFilterAndPageMarketplaceDirectorySearchTrimsAndIgnoresCase(t *testing.T) {
	directory := composedMarketplace{
		real: []composedRealModel{
			listPageRealWithDisplayName("model-a", "Alpha Chat", "OpenAI"),
			listPageReal("model-b", "OpenAI"),
		},
		routing: []RoutingModel{
			{ModelName: "route-a", DisplayName: "Alpha Router"},
			listPageRouting("route-b"),
		},
	}

	got, total := filterAndPageMarketplaceDirectory(
		directory,
		ListRequest{Search: "  ALPHA  "},
		1,
		20,
	)

	require.Equal(t, int64(2), total)
	require.Equal(t, []string{"real:model-a", "routing:route-a"}, listPageNames(got))
}

// Break caught: returning the zero directory for an out-of-range page would
// lose viewer identity or the pre-page total needed by the response.
func TestFilterAndPageMarketplaceDirectoryOutOfRangeKeepsViewerAndTotal(t *testing.T) {
	viewer := MarketplaceViewer{UserID: 7, GroupID: 3}
	directory := composedMarketplace{
		viewer:  viewer,
		real:    []composedRealModel{listPageReal("alpha", "OpenAI")},
		routing: []RoutingModel{listPageRouting("beta")},
	}

	got, total := filterAndPageMarketplaceDirectory(directory, ListRequest{}, 9, 20)

	require.Equal(t, int64(2), total)
	require.Equal(t, viewer, got.viewer)
	require.Empty(t, got.real)
	require.Empty(t, got.routing)
}

// Break caught: multiplying a representable maximum page by page size can
// overflow the int offset negative and panic while slicing the directory.
func TestFilterAndPageMarketplaceDirectoryOverflowReturnsEmptyPage(t *testing.T) {
	viewer := MarketplaceViewer{UserID: 7, GroupID: 3}
	directory := composedMarketplace{
		viewer:  viewer,
		real:    []composedRealModel{listPageReal("alpha", "OpenAI")},
		routing: []RoutingModel{listPageRouting("beta")},
	}

	got, total := filterAndPageMarketplaceDirectory(
		directory,
		ListRequest{},
		math.MaxInt,
		100,
	)

	require.Equal(t, int64(2), total)
	require.Equal(t, viewer, got.viewer)
	require.Empty(t, got.real)
	require.Empty(t, got.routing)
}

// Break caught: applying kind after pagination would let real entries consume
// positions in a routing-only page.
func TestFilterAndPageMarketplaceDirectoryRoutingOnly(t *testing.T) {
	directory := composedMarketplace{
		real: []composedRealModel{listPageReal("alpha", "OpenAI")},
		routing: []RoutingModel{
			listPageRouting("zeta"),
			listPageRouting("beta"),
		},
	}

	got, total := filterAndPageMarketplaceDirectory(
		directory,
		ListRequest{Kind: ModelKindRouting},
		1,
		1,
	)

	require.Equal(t, int64(2), total)
	require.Equal(t, []string{"routing:beta"}, listPageNames(got))
}

// Break caught: sorting or truncating shared input slices in place would make
// later detail/filter consumers observe a mutated full directory.
func TestFilterAndPageMarketplaceDirectoryDoesNotMutateInput(t *testing.T) {
	directory := composedMarketplace{
		real: []composedRealModel{
			listPageReal("zeta", "OpenAI"),
			listPageReal("alpha", "OpenAI"),
		},
		routing: []RoutingModel{
			listPageRouting("gamma"),
			listPageRouting("beta"),
		},
	}
	originalReal := []string{directory.real[0].model.ModelName, directory.real[1].model.ModelName}
	originalRouting := []string{directory.routing[0].ModelName, directory.routing[1].ModelName}

	got, total := filterAndPageMarketplaceDirectory(directory, ListRequest{}, 1, 2)

	require.Equal(t, int64(4), total)
	require.Equal(t, originalReal, []string{
		directory.real[0].model.ModelName,
		directory.real[1].model.ModelName,
	})
	require.Equal(t, originalRouting, []string{
		directory.routing[0].ModelName,
		directory.routing[1].ModelName,
	})
	require.NotSame(t, &directory.real[0], &got.real[0])
}

func listPageReal(name string, provider string) composedRealModel {
	return listPageRealWithDisplayName(name, name, provider)
}

func listPageRealWithDisplayName(name string, displayName string, provider string) composedRealModel {
	return composedRealModel{model: MarketplaceModel{
		ModelName: name,
		Metadata: models.ModelMetadata{
			DisplayName: displayName,
			Provider:    provider,
		},
	}}
}

func listPageRouting(name string) RoutingModel {
	return RoutingModel{ModelName: name, DisplayName: name}
}

func listPageNames(directory composedMarketplace) []string {
	type item struct {
		kind MarketplaceModelKind
		name string
	}
	items := make([]item, 0, len(directory.real)+len(directory.routing))
	for _, model := range directory.real {
		items = append(items, item{kind: ModelKindReal, name: model.model.ModelName})
	}
	for _, model := range directory.routing {
		items = append(items, item{kind: ModelKindRouting, name: model.ModelName})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].kind < items[j].kind
	})
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, string(item.kind)+":"+item.name)
	}
	return result
}
