package model_marketplace

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"golang.org/x/crypto/hkdf"
)

type ModelOfferKind string

const (
	OfferKindPlatform ModelOfferKind = "platform"
	OfferKindPrivate  ModelOfferKind = "private"
)

type ModelOfferOwnership string

const (
	OfferPlatform ModelOfferOwnership = "platform"
	OfferOwned    ModelOfferOwnership = "owned"
	OfferShared   ModelOfferOwnership = "shared"
)

type SupportedEndpoint string

const (
	EndpointChatCompletions SupportedEndpoint = "chat_completions"
	EndpointResponses       SupportedEndpoint = "responses"
	EndpointMessages        SupportedEndpoint = "messages"
	EndpointModels          SupportedEndpoint = "models"
)

// ModelOfferIdentity is the complete internal identity of one model/source
// offer. Task 7 uses it only after re-enumerating offers for the current viewer.
// Every field is fail-closed for JSON, even if Identity is marshaled directly.
type ModelOfferIdentity struct {
	ModelName string         `json:"-"`
	Kind      ModelOfferKind `json:"-"`
	SourceID  uint           `json:"-"`
}

// ModelOffer is the ordinary marketplace allowlist DTO. It deliberately copies
// only product-facing values and cannot serialize channel IDs, internal names,
// provider types, upstream paths, credentials, routing weights, or samples.
type ModelOffer struct {
	OfferRef           string              `json:"offer_ref"`
	Kind               ModelOfferKind      `json:"kind"`
	DisplayName        string              `json:"display_name"`
	Ownership          ModelOfferOwnership `json:"ownership"`
	Available          bool                `json:"available"`
	SupportedEndpoints []SupportedEndpoint `json:"supported_endpoints"`
	Identity           ModelOfferIdentity  `json:"-"`
	Facts              ModelOfferFacts     `json:"-"`
}

// ModelOfferFacts keeps pricing and administrator diagnostics collected by the
// existing batched channel queries. Every field fails closed if this internal
// value is accidentally marshaled; HTTP mappers copy only their allowlists.
type ModelOfferFacts struct {
	Billing           OfferBilling                 `json:"-"`
	InternalName      string                       `json:"-"`
	PublicDisplayName string                       `json:"-"`
	OwnerID           uint                         `json:"-"`
	BaseURL           string                       `json:"-"`
	EndpointPaths     map[SupportedEndpoint]string `json:"-"`
	DisabledReasons   []string                     `json:"-"`
}

// OfferRefEncoder creates and checks stable opaque offer references. Matches is
// intended for Task 7's current-viewer revalidation: regenerate visible
// candidates and compare instead of trusting a client-submitted reference.
type OfferRefEncoder interface {
	Encode(ModelOfferIdentity) (string, error)
	Matches(string, ModelOfferIdentity) bool
}

type hmacOfferRefEncoder struct {
	key []byte
}

// NewHMACOfferRefEncoder derives an offer-specific HMAC key from the master's
// existing jwt_secret lifecycle. The separate HKDF info prevents key reuse
// across JWT signing, BYOK encryption, and marketplace references.
func NewHMACOfferRefEncoder(jwtSecret string) (OfferRefEncoder, error) {
	if strings.TrimSpace(jwtSecret) == "" {
		return nil, errors.New("model marketplace offer ref secret is required")
	}
	key := make([]byte, sha256.Size)
	reader := hkdf.New(sha256.New, []byte(jwtSecret), nil, []byte("model-marketplace-offer-ref-v1"))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive model marketplace offer ref key: %w", err)
	}
	return &hmacOfferRefEncoder{key: key}, nil
}

func (e *hmacOfferRefEncoder) Encode(identity ModelOfferIdentity) (string, error) {
	prefix, discriminator, err := offerRefNamespace(identity.Kind)
	if err != nil {
		return "", err
	}
	if identity.ModelName == "" {
		return "", errors.New("model marketplace offer identity model name is required")
	}
	const (
		payloadVersion = byte(1)
		lengthSize     = 4
	)
	payload := make([]byte, 1+lengthSize+len(identity.ModelName)+1+8)
	payload[0] = payloadVersion
	binary.BigEndian.PutUint32(payload[1:1+lengthSize], uint32(len(identity.ModelName)))
	offset := 1 + lengthSize
	copy(payload[offset:], identity.ModelName)
	offset += len(identity.ModelName)
	payload[offset] = discriminator
	binary.BigEndian.PutUint64(payload[offset+1:], uint64(identity.SourceID))
	mac := hmac.New(sha256.New, e.key)
	_, _ = mac.Write(payload)
	return prefix + ":" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (e *hmacOfferRefEncoder) Matches(ref string, identity ModelOfferIdentity) bool {
	expected, err := e.Encode(identity)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(ref), []byte(expected)) == 1
}

func offerRefNamespace(kind ModelOfferKind) (string, byte, error) {
	switch kind {
	case OfferKindPlatform:
		return "p", 'p', nil
	case OfferKindPrivate:
		return "b", 'b', nil
	default:
		return "", 0, fmt.Errorf("unsupported model offer kind %q", kind)
	}
}

type PlatformModelOfferFinder struct {
	query   dao.ModelMarketplaceQuery
	encoder OfferRefEncoder
}

func NewPlatformModelOfferFinder(query dao.ModelMarketplaceQuery, encoder OfferRefEncoder) PlatformModelOfferFinder {
	return PlatformModelOfferFinder{query: query, encoder: encoder}
}

func (f PlatformModelOfferFinder) Find(
	ctx context.Context,
	viewer MarketplaceViewer,
	marketplaceModels []MarketplaceModel,
) (map[string][]ModelOffer, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return nil, err
	}
	if viewer.BYOKOnly {
		return map[string][]ModelOffer{}, nil
	}
	if f.query == nil {
		return nil, errors.New("platform model offer query is required")
	}
	if f.encoder == nil {
		return nil, errors.New("platform model offer ref encoder is required")
	}
	modelNames := allowedMarketplaceModelNames(viewer, marketplaceModels)
	if len(modelNames) == 0 {
		return map[string][]ModelOffer{}, nil
	}
	channels, err := f.query.ListMarketplaceChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("find platform marketplace channels: %w", err)
	}
	return f.compose(viewer, modelNames, channels)
}

type platformOfferCandidate struct {
	channelID   uint
	displayName string
	available   bool
	endpoints   []SupportedEndpoint
	facts       ModelOfferFacts
}

func (f PlatformModelOfferFinder) compose(
	viewer MarketplaceViewer,
	modelNames map[string]struct{},
	channels []models.Channel,
) (map[string][]ModelOffer, error) {
	adminGlobal := viewer.AdminGlobal
	candidates := make(map[string][]platformOfferCandidate, len(modelNames))
	for _, channel := range channels {
		enabled := channel.Status == consts.StatusEnabled
		if !adminGlobal && !viewer.AllowsChannel(channel.ID) {
			continue
		}
		endpoints := supportedEndpoints(channel.Endpoints)
		displayName := strings.TrimSpace(channel.PublicDisplayName)
		if displayName == "" {
			displayName = "平台来源"
		}
		for _, modelName := range platformChannelModelNames(channel.Models) {
			if _, allowed := modelNames[modelName]; !allowed {
				continue
			}
			candidates[modelName] = append(candidates[modelName], platformOfferCandidate{
				channelID: channel.ID, displayName: displayName, available: false, endpoints: endpoints,
				facts: ModelOfferFacts{
					InternalName: channel.Name, PublicDisplayName: channel.PublicDisplayName,
					BaseURL:         channel.BaseURL,
					EndpointPaths:   endpointPaths(channel.Endpoints),
					DisabledReasons: marketplaceOfferDisabledReasons(enabled, len(endpoints) > 0),
					Billing:         OfferBilling{PriceRatio: channel.PriceRatio, Free: channel.Free},
				},
			})
		}
	}

	offersByModel := make(map[string][]ModelOffer, len(candidates))
	for modelName, modelCandidates := range candidates {
		sort.Slice(modelCandidates, func(i, j int) bool {
			return modelCandidates[i].channelID < modelCandidates[j].channelID
		})
		nameCounts := make(map[string]int, len(modelCandidates))
		for _, candidate := range modelCandidates {
			nameCounts[candidate.displayName]++
		}
		nameIndexes := make(map[string]int, len(nameCounts))
		offers := make([]ModelOffer, 0, len(modelCandidates))
		for _, candidate := range modelCandidates {
			displayName := candidate.displayName
			if nameCounts[displayName] > 1 {
				nameIndexes[displayName]++
				displayName = fmt.Sprintf("%s · %d", displayName, nameIndexes[candidate.displayName])
			}
			identity := ModelOfferIdentity{ModelName: modelName, Kind: OfferKindPlatform, SourceID: candidate.channelID}
			ref, err := f.encoder.Encode(identity)
			if err != nil {
				return nil, fmt.Errorf("encode platform marketplace offer ref: %w", err)
			}
			facts := candidate.facts
			facts.Billing.Identity = identity
			offers = append(offers, ModelOffer{
				OfferRef: ref, Kind: OfferKindPlatform, DisplayName: displayName,
				Ownership: OfferPlatform, Available: candidate.available,
				SupportedEndpoints: cloneSupportedEndpoints(candidate.endpoints), Identity: identity, Facts: facts,
			})
		}
		offersByModel[modelName] = offers
	}
	return offersByModel, nil
}

type PrivateModelOfferFinder struct {
	query   dao.ModelMarketplaceQuery
	encoder OfferRefEncoder
}

func NewPrivateModelOfferFinder(query dao.ModelMarketplaceQuery, encoder OfferRefEncoder) PrivateModelOfferFinder {
	return PrivateModelOfferFinder{query: query, encoder: encoder}
}

func (f PrivateModelOfferFinder) Find(
	ctx context.Context,
	viewer MarketplaceViewer,
	marketplaceModels []MarketplaceModel,
) (map[string][]ModelOffer, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return nil, err
	}
	if f.query == nil {
		return nil, errors.New("private model offer query is required")
	}
	if f.encoder == nil {
		return nil, errors.New("private model offer ref encoder is required")
	}
	modelNames := allowedMarketplaceModelNames(viewer, marketplaceModels)
	if len(modelNames) == 0 {
		return map[string][]ModelOffer{}, nil
	}
	adminGlobal := viewer.AdminGlobal
	channels, err := f.query.ListMarketplacePrivateChannels(ctx, dao.MarketplacePrivateChannelScope{
		UserID: viewer.UserID, GroupIDs: append([]uint(nil), viewer.GroupIDs...), AdminGlobal: adminGlobal,
	})
	if err != nil {
		return nil, fmt.Errorf("find private marketplace channels: %w", err)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })

	offersByModel := make(map[string][]ModelOffer)
	for _, channel := range channels {
		enabled := channel.Status == consts.StatusEnabled
		endpoints := supportedEndpoints(channel.Endpoints)
		displayName := channel.Name
		if displayName == "" {
			displayName = "BYOK 来源"
		}
		ownership := OfferShared
		if viewer.UserID != 0 && channel.OwnerID == viewer.UserID {
			ownership = OfferOwned
		}
		for _, modelName := range privateChannelModelNames(channel.Models) {
			if _, allowed := modelNames[modelName]; !allowed {
				continue
			}
			identity := ModelOfferIdentity{ModelName: modelName, Kind: OfferKindPrivate, SourceID: channel.ID}
			ref, err := f.encoder.Encode(identity)
			if err != nil {
				return nil, fmt.Errorf("encode private marketplace offer ref: %w", err)
			}
			offersByModel[modelName] = append(offersByModel[modelName], ModelOffer{
				OfferRef: ref, Kind: OfferKindPrivate, DisplayName: displayName,
				Ownership: ownership, Available: false,
				SupportedEndpoints: cloneSupportedEndpoints(endpoints), Identity: identity,
				Facts: ModelOfferFacts{
					InternalName: channel.Name, OwnerID: channel.OwnerID,
					BaseURL:         channel.BaseURL,
					EndpointPaths:   endpointPaths(channel.Endpoints),
					DisabledReasons: marketplaceOfferDisabledReasons(enabled, len(endpoints) > 0),
					Billing:         OfferBilling{Identity: identity},
				},
			})
		}
	}
	return offersByModel, nil
}

func allowedMarketplaceModelNames(viewer MarketplaceViewer, marketplaceModels []MarketplaceModel) map[string]struct{} {
	result := make(map[string]struct{}, len(marketplaceModels))
	for _, marketplaceModel := range marketplaceModels {
		modelName := strings.TrimSpace(marketplaceModel.ModelName)
		if modelName == "" || !viewer.AllowedModels.Allows(modelName) {
			continue
		}
		result[modelName] = struct{}{}
	}
	return result
}

func platformChannelModelNames(raw string) []string {
	return normalizedMarketplaceModelNames(strings.Split(raw, ","))
}

func privateChannelModelNames(raw []string) []string {
	return normalizedMarketplaceModelNames(raw)
}

func normalizedMarketplaceModelNames(raw []string) []string {
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var supportedEndpointRegistry = []struct {
	key      string
	endpoint SupportedEndpoint
}{
	{key: "chat_completions", endpoint: EndpointChatCompletions},
	{key: "responses", endpoint: EndpointResponses},
	{key: "messages", endpoint: EndpointMessages},
	{key: "models", endpoint: EndpointModels},
}

func supportedEndpoints(raw string) []SupportedEndpoint {
	result := make([]SupportedEndpoint, 0, len(supportedEndpointRegistry))
	if strings.TrimSpace(raw) == "" {
		return result
	}
	var endpointPaths map[string]string
	if err := json.Unmarshal([]byte(raw), &endpointPaths); err != nil {
		return result
	}
	for _, entry := range supportedEndpointRegistry {
		if strings.TrimSpace(endpointPaths[entry.key]) != "" {
			result = append(result, entry.endpoint)
		}
	}
	return result
}

func endpointPaths(raw string) map[SupportedEndpoint]string {
	result := make(map[SupportedEndpoint]string)
	if strings.TrimSpace(raw) == "" {
		return result
	}
	var paths map[string]string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return result
	}
	for _, entry := range supportedEndpointRegistry {
		if path := strings.TrimSpace(paths[entry.key]); path != "" {
			result[entry.endpoint] = path
		}
	}
	return result
}

func marketplaceOfferDisabledReasons(enabled, endpointsConfigured bool) []string {
	reasons := make([]string, 0, 2)
	if !enabled {
		reasons = append(reasons, "disabled")
	}
	if !endpointsConfigured {
		reasons = append(reasons, "endpoints_not_configured")
	}
	return reasons
}

func cloneSupportedEndpoints(endpoints []SupportedEndpoint) []SupportedEndpoint {
	if len(endpoints) == 0 {
		return []SupportedEndpoint{}
	}
	return append([]SupportedEndpoint(nil), endpoints...)
}

func validateMarketplaceViewer(viewer MarketplaceViewer) error {
	if viewer.AdminGlobal {
		// behavior change: an explicit global administrator view is now rejected
		// when any token/user visibility constraints are accidentally mixed in.
		if viewer.UserID != 0 || viewer.Token != nil || viewer.GroupID != 0 ||
			viewer.GroupIDs != nil || viewer.TokenAllowedChannelIDs != nil ||
			viewer.GroupAllowedChannelIDs != nil || viewer.AllowedChannelIDs != nil ||
			viewer.AllowedModels.TokenPatterns != nil || viewer.AllowedModels.GroupPatterns != nil ||
			viewer.BYOKOnly {
			return errors.New("invalid marketplace viewer: admin global cannot carry token, user, group, or visibility scope")
		}
		return nil
	}
	// behavior change: ordinary marketplace calls are authorized only through a
	// persisted token whose owner exactly matches the viewer user scope.
	if viewer.Token == nil {
		return errors.New("invalid marketplace viewer: token scope is required")
	}
	if viewer.Token.ID == 0 {
		return errors.New("invalid marketplace viewer: token scope requires a persisted token")
	}
	if viewer.UserID != viewer.Token.UserID {
		return errors.New("invalid marketplace viewer: token owner does not match viewer")
	}
	return nil
}
