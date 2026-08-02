package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/stretchr/testify/require"
)

func TestOfferUsageReferenceFinderReturnsSelectedAndOwnedScopes(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	platform := usageOffer("platform", OfferKindPlatform, OfferPlatform, "gpt-4o", 11)
	owned := usageOffer("owned", OfferKindPrivate, OfferOwned, "gpt-4o", 21)
	shared := usageOffer("shared", OfferKindPrivate, OfferShared, "gpt-4o", 22)
	query := &fakeMarketplaceUsageQuery{
		selected: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
			usageDAOOffer(platform): {InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, ReferenceCost: int64PointerForUsageTest(100), GatewayChargeCost: 50},
			usageDAOOffer(owned):    {InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, ReferenceCost: int64PointerForUsageTest(200), GatewayChargeCost: 20},
		},
		owner: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
			usageDAOOffer(owned): {InputTokens: 100, OutputTokens: 200, CacheReadTokens: 300, CacheWriteTokens: 400, ReferenceCost: int64PointerForUsageTest(2_000), GatewayChargeCost: 200},
		},
	}
	finder := NewOfferUsageReferenceFinder(query)
	finder.now = func() time.Time { return now }

	got, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{platform, owned, shared}, UsageWindow7Days)
	require.NoError(t, err)
	require.Equal(t, 1, query.selectedCalls)
	require.Equal(t, 1, query.ownerCalls)
	require.Zero(t, query.adminCalls)
	require.Equal(t, now.Add(-7*24*time.Hour).Unix(), query.lastSelected.Range.Start)
	require.Equal(t, now.Unix(), query.lastSelected.Range.End)
	require.Len(t, query.lastOwner.Offers, 1)
	require.Equal(t, uint(7), query.lastOwner.ViewerUserID)

	require.Equal(t, []OfferUsageReference{{
		Scope: UsageScopeSelectedToken, Window: UsageWindow7Days,
		TokenUnits:    TokenUnits{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10},
		ReferenceCost: int64PointerForUsageTest(100), GatewayChargeCost: 50, EstimatedTotalCost: int64PointerForUsageTest(50), Accuracy: AccuracyExact,
	}}, got[platform.OfferRef])
	require.Equal(t, []OfferUsageReference{
		{
			Scope: UsageScopeSelectedToken, Window: UsageWindow7Days,
			TokenUnits:    TokenUnits{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100},
			ReferenceCost: int64PointerForUsageTest(200), GatewayChargeCost: 20, EstimatedTotalCost: int64PointerForUsageTest(220), Accuracy: AccuracyReference,
		},
		{
			Scope: UsageScopeOwnerChannelTotal, Window: UsageWindow7Days,
			TokenUnits:    TokenUnits{Input: 100, Output: 200, CacheRead: 300, CacheWrite: 400, Total: 1_000},
			ReferenceCost: int64PointerForUsageTest(2_000), GatewayChargeCost: 200, EstimatedTotalCost: int64PointerForUsageTest(2_200),
			Accuracy: AccuracyReference, IncludesSharedUsage: true,
		},
	}, got[owned.OfferRef])
	require.Equal(t, []OfferUsageReference{{
		Scope: UsageScopeSelectedToken, Window: UsageWindow7Days,
		TokenUnits: TokenUnits{}, ReferenceCost: int64PointerForUsageTest(0),
		EstimatedTotalCost: int64PointerForUsageTest(0), Accuracy: AccuracyReference,
	}}, got[shared.OfferRef], "visible offers with no calls still receive a zero selected-token reference")
}

func TestOfferUsageReferenceFinderNeverReturnsOwnerTotalsToSharedRecipient(t *testing.T) {
	shared := usageOffer("shared", OfferKindPrivate, OfferShared, "gpt-4o", 22)
	query := &fakeMarketplaceUsageQuery{}
	finder := NewOfferUsageReferenceFinder(query)
	finder.now = func() time.Time { return time.Unix(1_000_000, 0) }

	got, err := finder.Find(context.Background(), scopedMarketplaceViewer(9, 90), []ModelOffer{shared}, UsageWindow24Hours)
	require.NoError(t, err)
	require.Equal(t, 1, query.selectedCalls)
	require.Zero(t, query.ownerCalls)
	require.Equal(t, UsageScopeSelectedToken, got[shared.OfferRef][0].Scope)
	require.False(t, got[shared.OfferRef][0].IncludesSharedUsage)
}

func TestOfferUsageReferenceFinderReturnsOnlyOfferTotalForAdmin(t *testing.T) {
	platform := usageOffer("platform", OfferKindPlatform, OfferPlatform, "gpt-4o", 11)
	private := usageOffer("private", OfferKindPrivate, OfferShared, "gpt-4o", 21)
	query := &fakeMarketplaceUsageQuery{admin: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
		usageDAOOffer(platform): {InputTokens: 1, ReferenceCost: int64PointerForUsageTest(10), GatewayChargeCost: 5},
		usageDAOOffer(private):  {OutputTokens: 2, ReferenceCost: int64PointerForUsageTest(20), GatewayChargeCost: 3},
	}}
	finder := NewOfferUsageReferenceFinder(query)
	finder.now = func() time.Time { return time.Unix(1_000_000, 0) }

	got, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true}, []ModelOffer{platform, private}, UsageWindow30Days)
	require.NoError(t, err)
	require.Zero(t, query.selectedCalls)
	require.Zero(t, query.ownerCalls)
	require.Equal(t, 1, query.adminCalls)
	require.Equal(t, OfferUsageReference{
		Scope: UsageScopeOfferTotal, Window: UsageWindow30Days,
		TokenUnits: TokenUnits{Input: 1, Total: 1}, ReferenceCost: int64PointerForUsageTest(10), GatewayChargeCost: 5,
		EstimatedTotalCost: int64PointerForUsageTest(5), Accuracy: AccuracyExact,
	}, got[platform.OfferRef][0])
	require.Equal(t, int64PointerForUsageTest(23), got[private.OfferRef][0].EstimatedTotalCost)
	require.Equal(t, AccuracyReference, got[private.OfferRef][0].Accuracy)
}

func TestOfferUsageReferenceFinderReturnsNullForIncompletePrivateReference(t *testing.T) {
	private := usageOffer("private", OfferKindPrivate, OfferOwned, "gpt-4o", 21)
	query := &fakeMarketplaceUsageQuery{selected: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
		usageDAOOffer(private): {InputTokens: 1, ReferenceCost: nil, GatewayChargeCost: 7},
	}}
	finder := NewOfferUsageReferenceFinder(query)
	finder.now = func() time.Time { return time.Unix(1_000_000, 0) }

	got, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{private}, UsageWindow24Hours)

	require.NoError(t, err)
	reference := got[private.OfferRef][0]
	require.Nil(t, reference.ReferenceCost)
	require.Nil(t, reference.EstimatedTotalCost)
	require.Equal(t, int64(7), reference.GatewayChargeCost)
	payload, err := json.Marshal(reference)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"scope":"selected_token","window":"24h",
		"token_units":{"input":1,"output":0,"cache_read":0,"cache_write":0,"total":1},
		"reference_cost":null,"gateway_charge_cost":7,"estimated_total_cost":null,
		"accuracy":"reference","includes_shared_usage":false
	}`, string(payload))
}

func TestOfferUsageReferenceFinderRejectsNonNegativeInt64Overflow(t *testing.T) {
	platform := usageOffer("platform", OfferKindPlatform, OfferPlatform, "gpt-4o", 11)
	private := usageOffer("private", OfferKindPrivate, OfferOwned, "gpt-4o", 21)
	tests := []struct {
		name    string
		offer   ModelOffer
		totals  dao.MarketplaceUsageTotals
		wantErr string
	}{
		{name: "four bucket total overflow", offer: platform, totals: dao.MarketplaceUsageTotals{InputTokens: math.MaxInt64, OutputTokens: 1, ReferenceCost: int64PointerForUsageTest(0)}, wantErr: "overflow"},
		{name: "intermediate four bucket overflow", offer: platform, totals: dao.MarketplaceUsageTotals{InputTokens: math.MaxInt64 - 1, OutputTokens: 1, CacheReadTokens: 1, ReferenceCost: int64PointerForUsageTest(0)}, wantErr: "overflow"},
		{name: "private estimated total overflow", offer: private, totals: dao.MarketplaceUsageTotals{ReferenceCost: int64PointerForUsageTest(math.MaxInt64), GatewayChargeCost: 1}, wantErr: "overflow"},
		{name: "negative token input", offer: platform, totals: dao.MarketplaceUsageTotals{InputTokens: -1, ReferenceCost: int64PointerForUsageTest(0)}, wantErr: "non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &fakeMarketplaceUsageQuery{selected: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
				usageDAOOffer(tt.offer): tt.totals,
			}}
			finder := NewOfferUsageReferenceFinder(query)
			finder.now = func() time.Time { return time.Unix(1_000_000, 0) }

			got, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{tt.offer}, UsageWindow24Hours)

			require.Nil(t, got)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestOfferUsageReferenceFinderAcceptsExactInt64Maximum(t *testing.T) {
	private := usageOffer("private", OfferKindPrivate, OfferOwned, "gpt-4o", 21)
	query := &fakeMarketplaceUsageQuery{selected: map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals{
		usageDAOOffer(private): {
			InputTokens: math.MaxInt64 - 1, OutputTokens: 1,
			ReferenceCost: int64PointerForUsageTest(math.MaxInt64 - 1), GatewayChargeCost: 1,
		},
	}}
	finder := NewOfferUsageReferenceFinder(query)
	finder.now = func() time.Time { return time.Unix(1_000_000, 0) }

	got, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{private}, UsageWindow24Hours)

	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), got[private.OfferRef][0].TokenUnits.Total)
	require.Equal(t, int64PointerForUsageTest(math.MaxInt64), got[private.OfferRef][0].EstimatedTotalCost)
}

func TestOfferUsageReferenceFinderRejectsInvalidWindowViewerAndOfferBeforeDAO(t *testing.T) {
	validOffer := usageOffer("platform", OfferKindPlatform, OfferPlatform, "gpt-4o", 11)
	tests := []struct {
		name    string
		viewer  MarketplaceViewer
		offers  []ModelOffer
		window  UsageWindow
		wantErr string
	}{
		{name: "invalid window", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{validOffer}, window: "1h", wantErr: "usage window"},
		{name: "invalid viewer", viewer: MarketplaceViewer{}, offers: []ModelOffer{validOffer}, window: UsageWindow24Hours, wantErr: "marketplace viewer"},
		{name: "incomplete identity", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{{OfferRef: "broken", Kind: OfferKindPlatform}}, window: UsageWindow24Hours, wantErr: "offer identity"},
		{name: "empty model", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{usageOffer("empty", OfferKindPlatform, OfferPlatform, "", 11)}, window: UsageWindow24Hours, wantErr: "model"},
		{name: "blank model", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{usageOffer("blank", OfferKindPlatform, OfferPlatform, "   ", 11)}, window: UsageWindow24Hours, wantErr: "model"},
		{name: "untrimmed model", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{usageOffer("untrimmed", OfferKindPlatform, OfferPlatform, " gpt-4o ", 11)}, window: UsageWindow24Hours, wantErr: "model"},
		{name: "duplicate ref conflict", viewer: scopedMarketplaceViewer(7, 70), offers: []ModelOffer{validOffer, usageOffer("platform", OfferKindPlatform, OfferPlatform, "other", 12)}, window: UsageWindow24Hours, wantErr: "offer reference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &fakeMarketplaceUsageQuery{}
			finder := NewOfferUsageReferenceFinder(query)
			_, err := finder.Find(context.Background(), tt.viewer, tt.offers, tt.window)
			require.ErrorContains(t, err, tt.wantErr)
			require.Zero(t, query.selectedCalls+query.ownerCalls+query.adminCalls)
		})
	}
}

func TestOfferUsageReferenceFinderPropagatesScopeQueryFailures(t *testing.T) {
	platform := usageOffer("platform", OfferKindPlatform, OfferPlatform, "gpt-4o", 11)
	owned := usageOffer("owned", OfferKindPrivate, OfferOwned, "gpt-4o", 21)
	t.Run("selected", func(t *testing.T) {
		query := &fakeMarketplaceUsageQuery{selectedErr: errors.New("selected failed")}
		finder := NewOfferUsageReferenceFinder(query)
		_, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{platform}, UsageWindow24Hours)
		require.ErrorContains(t, err, "selected failed")
		require.Zero(t, query.ownerCalls)
	})
	t.Run("owner", func(t *testing.T) {
		query := &fakeMarketplaceUsageQuery{ownerErr: errors.New("owner failed")}
		finder := NewOfferUsageReferenceFinder(query)
		_, err := finder.Find(context.Background(), scopedMarketplaceViewer(7, 70), []ModelOffer{owned}, UsageWindow24Hours)
		require.ErrorContains(t, err, "owner failed")
	})
	t.Run("admin", func(t *testing.T) {
		query := &fakeMarketplaceUsageQuery{adminErr: errors.New("admin failed")}
		finder := NewOfferUsageReferenceFinder(query)
		_, err := finder.Find(context.Background(), MarketplaceViewer{AdminGlobal: true}, []ModelOffer{platform}, UsageWindow24Hours)
		require.ErrorContains(t, err, "admin failed")
	})
}

type fakeMarketplaceUsageQuery struct {
	selected map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals
	owner    map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals
	admin    map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals

	selectedErr error
	ownerErr    error
	adminErr    error

	selectedCalls int
	ownerCalls    int
	adminCalls    int
	lastSelected  dao.SelectedTokenUsageScope
	lastOwner     dao.OwnerChannelUsageScope
	lastAdmin     dao.AdminOfferUsageScope
}

func (q *fakeMarketplaceUsageQuery) FindSelectedTokenUsage(_ context.Context, scope dao.SelectedTokenUsageScope) (map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals, error) {
	q.selectedCalls++
	q.lastSelected = scope
	return q.selected, q.selectedErr
}

func (q *fakeMarketplaceUsageQuery) FindOwnerChannelUsage(_ context.Context, scope dao.OwnerChannelUsageScope) (map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals, error) {
	q.ownerCalls++
	q.lastOwner = scope
	return q.owner, q.ownerErr
}

func (q *fakeMarketplaceUsageQuery) FindAdminOfferUsage(_ context.Context, scope dao.AdminOfferUsageScope) (map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals, error) {
	q.adminCalls++
	q.lastAdmin = scope
	return q.admin, q.adminErr
}

func usageOffer(ref string, kind ModelOfferKind, ownership ModelOfferOwnership, modelName string, sourceID uint) ModelOffer {
	return ModelOffer{
		OfferRef: ref, Kind: kind, Ownership: ownership, Available: true,
		SupportedEndpoints: []SupportedEndpoint{EndpointChatCompletions},
		Identity:           ModelOfferIdentity{ModelName: modelName, Kind: kind, SourceID: sourceID},
	}
}

func usageDAOOffer(offer ModelOffer) dao.MarketplaceUsageOffer {
	kind := dao.MarketplaceUsageOfferPlatform
	if offer.Kind == OfferKindPrivate {
		kind = dao.MarketplaceUsageOfferPrivate
	}
	return dao.MarketplaceUsageOffer{ModelName: offer.Identity.ModelName, Kind: kind, SourceID: offer.Identity.SourceID}
}

var _ dao.ModelMarketplaceUsageQuery = (*fakeMarketplaceUsageQuery)(nil)

func int64PointerForUsageTest(value int64) *int64 { return &value }
