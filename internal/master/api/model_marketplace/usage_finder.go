package model_marketplace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/safeint"
)

type UsageScope string

const (
	UsageScopeSelectedToken     UsageScope = "selected_token"
	UsageScopeOwnerChannelTotal UsageScope = "owner_channel_total"
	UsageScopeOfferTotal        UsageScope = "offer_total"
)

type UsageWindow string

const (
	UsageWindow24Hours UsageWindow = "24h"
	UsageWindow7Days   UsageWindow = "7d"
	UsageWindow30Days  UsageWindow = "30d"
)

type TokenUnits struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

// OfferUsageReference costs use the existing integer settlement unit. The
// gateway currently defines 100000 units as one USD; no conversion or extra
// rounding is performed here. BYOK reference costs are local ModelConfig-based
// estimates rather than supplier invoice facts.
type OfferUsageReference struct {
	Scope               UsageScope    `json:"scope"`
	Window              UsageWindow   `json:"window"`
	TokenUnits          TokenUnits    `json:"token_units"`
	ReferenceCost       *int64        `json:"reference_cost"`
	GatewayChargeCost   int64         `json:"gateway_charge_cost"`
	EstimatedTotalCost  *int64        `json:"estimated_total_cost"`
	Accuracy            OfferAccuracy `json:"accuracy"`
	IncludesSharedUsage bool          `json:"includes_shared_usage"`
}

type OfferUsageReferenceFinder struct {
	query dao.ModelMarketplaceUsageQuery
	now   func() time.Time
}

func NewOfferUsageReferenceFinder(query dao.ModelMarketplaceUsageQuery) OfferUsageReferenceFinder {
	return OfferUsageReferenceFinder{query: query, now: time.Now}
}

// Find returns usage references keyed by opaque offer_ref. The input must be
// the complete server-side offers re-enumerated for viewer; every identity and
// reference is validated again before any DAO call.
func (f OfferUsageReferenceFinder) Find(
	ctx context.Context,
	viewer MarketplaceViewer,
	offers []ModelOffer,
	window UsageWindow,
) (map[string][]OfferUsageReference, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return nil, err
	}
	duration, err := usageWindowDuration(window)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalUsageOffers(offers)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 {
		return map[string][]OfferUsageReference{}, nil
	}
	if f.query == nil {
		return nil, errors.New("marketplace usage query is required")
	}
	if f.now == nil {
		return nil, errors.New("marketplace usage clock is required")
	}
	end := f.now().Unix()
	usageRange := dao.MarketplaceUsageRange{Start: end - int64(duration/time.Second), End: end}
	daoOffers := make([]dao.MarketplaceUsageOffer, 0, len(canonical))
	for _, offer := range canonical {
		daoOffers = append(daoOffers, marketplaceDAOUsageOffer(offer))
	}

	if viewer.AdminGlobal {
		return f.findAdminUsageReferences(ctx, canonical, daoOffers, usageRange, window)
	}
	return f.findViewerUsageReferences(ctx, viewer, canonical, daoOffers, usageRange, window)
}

func (f OfferUsageReferenceFinder) findAdminUsageReferences(
	ctx context.Context,
	offers []ModelOffer,
	daoOffers []dao.MarketplaceUsageOffer,
	usageRange dao.MarketplaceUsageRange,
	window UsageWindow,
) (map[string][]OfferUsageReference, error) {
	totals, err := f.query.FindAdminOfferUsage(ctx, dao.AdminOfferUsageScope{Offers: daoOffers, Range: usageRange})
	if err != nil {
		return nil, fmt.Errorf("find administrator marketplace offer usage: %w", err)
	}
	references, err := usageReferencesForScope(offers, totals, UsageScopeOfferTotal, window, false)
	if err != nil {
		return nil, fmt.Errorf("build administrator marketplace usage reference: %w", err)
	}
	return references, nil
}

func (f OfferUsageReferenceFinder) findViewerUsageReferences(
	ctx context.Context,
	viewer MarketplaceViewer,
	offers []ModelOffer,
	daoOffers []dao.MarketplaceUsageOffer,
	usageRange dao.MarketplaceUsageRange,
	window UsageWindow,
) (map[string][]OfferUsageReference, error) {
	selected, err := f.query.FindSelectedTokenUsage(ctx, dao.SelectedTokenUsageScope{
		UserID: viewer.UserID, TokenID: viewer.Token.ID, Offers: daoOffers, Range: usageRange,
	})
	if err != nil {
		return nil, fmt.Errorf("find selected-token marketplace offer usage: %w", err)
	}
	result, err := usageReferencesForScope(offers, selected, UsageScopeSelectedToken, window, false)
	if err != nil {
		return nil, fmt.Errorf("build selected-token marketplace usage reference: %w", err)
	}

	owned := ownedPrivateUsageOffers(offers)
	if len(owned) == 0 {
		return result, nil
	}
	ownedDAO := make([]dao.MarketplaceUsageOffer, 0, len(owned))
	for _, offer := range owned {
		ownedDAO = append(ownedDAO, marketplaceDAOUsageOffer(offer))
	}
	ownerTotals, err := f.query.FindOwnerChannelUsage(ctx, dao.OwnerChannelUsageScope{
		ViewerUserID: viewer.UserID, Offers: ownedDAO, Range: usageRange,
	})
	if err != nil {
		return nil, fmt.Errorf("find owned-channel marketplace usage: %w", err)
	}
	ownerReferences, err := usageReferencesForScope(owned, ownerTotals, UsageScopeOwnerChannelTotal, window, true)
	if err != nil {
		return nil, fmt.Errorf("build owned-channel marketplace usage reference: %w", err)
	}
	for ref, references := range ownerReferences {
		result[ref] = append(result[ref], references...)
	}
	return result, nil
}

func usageWindowDuration(window UsageWindow) (time.Duration, error) {
	switch window {
	case UsageWindow24Hours:
		return 24 * time.Hour, nil
	case UsageWindow7Days:
		return 7 * 24 * time.Hour, nil
	case UsageWindow30Days:
		return 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid marketplace usage window %q", window)
	}
}

func canonicalUsageOffers(offers []ModelOffer) ([]ModelOffer, error) {
	if len(offers) == 0 {
		return []ModelOffer{}, nil
	}
	result := append([]ModelOffer(nil), offers...)
	byRef := make(map[string]ModelOfferIdentity, len(result))
	byIdentity := make(map[ModelOfferIdentity]string, len(result))
	for _, offer := range result {
		if err := validateRoutingOffer(offer.Identity.ModelName, offer); err != nil {
			return nil, err
		}
		modelName := strings.TrimSpace(offer.Identity.ModelName)
		if modelName == "" || modelName != offer.Identity.ModelName {
			return nil, errors.New("invalid marketplace offer model name")
		}
		if err := validateUsageOfferOwnership(offer); err != nil {
			return nil, err
		}
		if previous, duplicate := byRef[offer.OfferRef]; duplicate {
			if previous != offer.Identity {
				return nil, errors.New("conflicting marketplace offer reference")
			}
			return nil, errors.New("duplicate marketplace offer reference")
		}
		byRef[offer.OfferRef] = offer.Identity
		if previous, duplicate := byIdentity[offer.Identity]; duplicate {
			if previous != offer.OfferRef {
				return nil, errors.New("conflicting marketplace offer identity")
			}
			return nil, errors.New("duplicate marketplace offer identity")
		}
		byIdentity[offer.Identity] = offer.OfferRef
	}
	return result, nil
}

func validateUsageOfferOwnership(offer ModelOffer) error {
	switch offer.Kind {
	case OfferKindPlatform:
		if offer.Ownership != OfferPlatform {
			return errors.New("invalid platform marketplace offer ownership")
		}
	case OfferKindPrivate:
		if offer.Ownership != OfferOwned && offer.Ownership != OfferShared {
			return errors.New("invalid private marketplace offer ownership")
		}
	default:
		return fmt.Errorf("unsupported marketplace offer kind %q", offer.Kind)
	}
	return nil
}

func marketplaceDAOUsageOffer(offer ModelOffer) dao.MarketplaceUsageOffer {
	kind := dao.MarketplaceUsageOfferPlatform
	if offer.Kind == OfferKindPrivate {
		kind = dao.MarketplaceUsageOfferPrivate
	}
	return dao.MarketplaceUsageOffer{
		ModelName: offer.Identity.ModelName, Kind: kind, SourceID: offer.Identity.SourceID,
	}
}

func ownedPrivateUsageOffers(offers []ModelOffer) []ModelOffer {
	result := make([]ModelOffer, 0, len(offers))
	for _, offer := range offers {
		if offer.Kind == OfferKindPrivate && offer.Ownership == OfferOwned {
			result = append(result, offer)
		}
	}
	return result
}

func usageReferencesForScope(
	offers []ModelOffer,
	totals map[dao.MarketplaceUsageOffer]dao.MarketplaceUsageTotals,
	scope UsageScope,
	window UsageWindow,
	includesShared bool,
) (map[string][]OfferUsageReference, error) {
	result := make(map[string][]OfferUsageReference, len(offers))
	for _, offer := range offers {
		total, exists := totals[marketplaceDAOUsageOffer(offer)]
		if !exists {
			zero := int64(0)
			total.ReferenceCost = &zero
		}
		reference, err := usageReference(offer.Kind, total, scope, window, includesShared)
		if err != nil {
			return nil, fmt.Errorf("offer %q: %w", offer.OfferRef, err)
		}
		result[offer.OfferRef] = []OfferUsageReference{
			reference,
		}
	}
	return result, nil
}

func usageReference(
	kind ModelOfferKind,
	totals dao.MarketplaceUsageTotals,
	scope UsageScope,
	window UsageWindow,
	includesShared bool,
) (OfferUsageReference, error) {
	tokenUnits := TokenUnits{
		Input: totals.InputTokens, Output: totals.OutputTokens,
		CacheRead: totals.CacheReadTokens, CacheWrite: totals.CacheWriteTokens,
	}
	var err error
	tokenUnits.Total, err = safeint.AddNonNegativeInt64(
		tokenUnits.Input,
		tokenUnits.Output,
		tokenUnits.CacheRead,
		tokenUnits.CacheWrite,
	)
	if err != nil {
		return OfferUsageReference{}, fmt.Errorf("sum token units: %w", err)
	}
	gatewayCharge, err := safeint.AddNonNegativeInt64(totals.GatewayChargeCost)
	if err != nil {
		return OfferUsageReference{}, fmt.Errorf("validate gateway charge: %w", err)
	}
	var referenceCost *int64
	if totals.ReferenceCost != nil {
		value, referenceErr := safeint.AddNonNegativeInt64(*totals.ReferenceCost)
		if referenceErr != nil {
			return OfferUsageReference{}, fmt.Errorf("validate reference cost: %w", referenceErr)
		}
		referenceCost = &value
	}
	reference := OfferUsageReference{
		Scope: scope, Window: window, TokenUnits: tokenUnits,
		ReferenceCost: referenceCost, GatewayChargeCost: gatewayCharge,
		IncludesSharedUsage: includesShared,
	}
	if kind == OfferKindPrivate {
		reference.Accuracy = AccuracyReference
		if referenceCost == nil {
			return reference, nil
		}
		estimated, addErr := safeint.AddNonNegativeInt64(*referenceCost, gatewayCharge)
		if addErr != nil {
			return OfferUsageReference{}, fmt.Errorf("sum private estimated total: %w", addErr)
		}
		reference.EstimatedTotalCost = &estimated
		return reference, nil
	}
	reference.Accuracy = AccuracyExact
	reference.EstimatedTotalCost = &gatewayCharge
	return reference, nil
}
