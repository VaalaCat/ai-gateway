package token

import "github.com/VaalaCat/ai-gateway/internal/models"

const systemTokenOwnerUsername = "System"

// ListResponse is the Token list DTO. It adds the owner display information
// needed by administrative pickers without changing single-Token responses.
type ListResponse struct {
	models.Token
	OwnerUsername string `json:"owner_username"`
}

func projectListResponses(tokens []models.Token, owners []models.User) []ListResponse {
	ownerUsernames := make(map[uint]string, len(owners))
	for _, owner := range owners {
		ownerUsernames[owner.ID] = owner.Username
	}
	responses := make([]ListResponse, len(tokens))
	for i, token := range tokens {
		ownerUsername := ownerUsernames[token.UserID]
		if token.UserID == 0 {
			ownerUsername = systemTokenOwnerUsername
		}
		responses[i] = ListResponse{
			Token:         token,
			OwnerUsername: ownerUsername,
		}
	}
	return responses
}

func missingTokenOwnerID(tokens []models.Token, owners []models.User) (uint, bool) {
	ownerIDs := make(map[uint]struct{}, len(owners))
	for _, owner := range owners {
		ownerIDs[owner.ID] = struct{}{}
	}
	for _, token := range tokens {
		if token.UserID == 0 {
			continue
		}
		if _, found := ownerIDs[token.UserID]; !found {
			return token.UserID, true
		}
	}
	return 0, false
}
