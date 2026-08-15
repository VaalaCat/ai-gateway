package sync

import (
	"encoding/base64"
	"errors"

	apiupstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

var errAPIProjection = errors.New("project API execution data failed")

type APIProjector struct {
	cipher *byokcrypto.Cipher
}

func NewAPIProjector(cipher *byokcrypto.Cipher) *APIProjector {
	return &APIProjector{cipher: cipher}
}

func (p *APIProjector) ProjectService(service models.APIService) protocol.SyncedAPIService {
	return protocol.SyncedAPIService{
		ID: service.ID, Slug: service.Slug, Name: service.Name,
		ConsumesQuota: service.PricePerCall > 0, Status: service.Status,
	}
}

func (p *APIProjector) ProjectRoute(route models.APIRoute) protocol.SyncedAPIRoute {
	protocols := make([]string, len(route.Protocols))
	for i, item := range route.Protocols {
		protocols[i] = string(item)
	}
	return protocol.SyncedAPIRoute{
		ID: route.ID, ServiceID: route.APIServiceID, BackendID: route.BackendID, Slug: route.Slug,
		Protocols: protocols, AllowedMethods: append([]string(nil), route.AllowedMethods...),
		WebSocketSubprotocols: append([]string(nil), route.WebSocketSubprotocols...),
		UpstreamPath:          route.UpstreamPath, ForwardSubpath: route.ForwardSubpath, Status: route.Status,
	}
}

func (p *APIProjector) ProjectUpstream(upstream models.APIUpstream) (protocol.SyncedAPIUpstream, error) {
	credential, err := p.projectCredential(upstream)
	if err != nil {
		return protocol.SyncedAPIUpstream{}, errAPIProjection
	}
	proxyURL, err := p.decryptOptional(upstream.ProxyURLCiphertext, upstream.ID)
	if err != nil {
		return protocol.SyncedAPIUpstream{}, errAPIProjection
	}
	headers := upstream.HeaderOverride.Data()
	return protocol.SyncedAPIUpstream{
		ID: upstream.ID, BackendID: upstream.BackendID, Name: upstream.Name, BaseURL: upstream.BaseURL,
		AuthType: string(upstream.AuthType), Credential: credential, HeaderOverride: cloneStringMap(headers),
		ProxyURL: proxyURL, Priority: upstream.Priority, Weight: upstream.Weight, Status: upstream.Status,
	}, nil
}

func (p *APIProjector) projectCredential(upstream models.APIUpstream) (protocol.APIUpstreamCredential, error) {
	if upstream.CredentialCiphertext == "" {
		if upstream.AuthType == models.APIUpstreamAuthNone {
			return protocol.APIUpstreamCredential{}, nil
		}
		return protocol.APIUpstreamCredential{}, errAPIProjection
	}
	return apiupstream.DecryptAPIUpstreamCredential(
		p.cipher, upstream.ID, upstream.AuthType, upstream.CredentialCiphertext,
	)
}

func (p *APIProjector) decryptOptional(ciphertext string, ownerID uint) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if p.cipher == nil {
		return "", errAPIProjection
	}
	sealed, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", errAPIProjection
	}
	plaintext, err := p.cipher.Open(sealed, ownerID)
	if err != nil {
		return "", errAPIProjection
	}
	return plaintext, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
