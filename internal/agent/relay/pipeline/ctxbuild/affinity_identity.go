package ctxbuild

import (
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
)

type affinityIdentityInput struct {
	Header http.Header
	Body   []byte
}

type affinityIdentityFinder interface {
	Find(affinityIdentityInput) (state.AffinityIdentity, bool)
}

var affinityIdentityFinders = []affinityIdentityFinder{
	headerAffinityIdentityFinder{
		name: consts.HeaderSessionID,
		partition: state.AffinityPartition{
			ByUser: true, ByToken: true, ByModel: true,
		},
	},
}

func findAffinityIdentity(input affinityIdentityInput) (state.AffinityIdentity, bool) {
	for _, finder := range affinityIdentityFinders {
		if identity, ok := finder.Find(input); ok {
			return identity, true
		}
	}
	return state.AffinityIdentity{}, false
}

type headerAffinityIdentityFinder struct {
	name      string
	partition state.AffinityPartition
}

func (finder headerAffinityIdentityFinder) Find(input affinityIdentityInput) (state.AffinityIdentity, bool) {
	key := strings.TrimSpace(input.Header.Get(finder.name))
	if key == "" {
		return state.AffinityIdentity{}, false
	}
	return state.AffinityIdentity{Key: key, Partition: finder.partition}, true
}
