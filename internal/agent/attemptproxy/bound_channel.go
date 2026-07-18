package attemptproxy

import (
	"errors"
	"slices"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/executionmode"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

const (
	boundSourceInvalid    = "bound_source_invalid"
	boundChannelNotFound  = "bound_channel_not_found"
	boundChannelForbidden = "bound_channel_forbidden"
	boundModelForbidden   = "bound_model_forbidden"
	boundModeMismatch     = "bound_mode_mismatch"
)

type BoundChannelInput struct {
	User            *app.UserInfo
	Attempt         attemptwire.BoundAttempt
	InboundProtocol codec.Protocol
}

type BoundChannelFinder interface {
	Find(BoundChannelInput) (state.Attempt, error)
}

type channelFinder struct {
	cache app.AgentCache
}

type BoundChannelError struct {
	Code string
}

func (e *BoundChannelError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func BoundChannelErrorCode(err error) string {
	var target *BoundChannelError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func NewBoundChannelFinder(cache app.AgentCache) BoundChannelFinder {
	return &channelFinder{cache: cache}
}

func (f *channelFinder) Find(input BoundChannelInput) (state.Attempt, error) {
	if err := input.Attempt.Validate(); err != nil {
		return state.Attempt{}, invalidAttemptError(input.Attempt)
	}
	if input.User == nil {
		return state.Attempt{}, boundChannelError(boundChannelForbidden)
	}
	if err := auth.AuthorizeModel(input.User, input.Attempt.RealModel); err != nil {
		return state.Attempt{}, boundChannelError(boundModelForbidden)
	}

	channel, err := f.findChannel(input.User, input.Attempt)
	if err != nil {
		return state.Attempt{}, err
	}
	if executionmode.ForChannel(channel, input.Attempt.RealModel, input.InboundProtocol) != input.Attempt.Mode {
		return state.Attempt{}, boundChannelError(boundModeMismatch)
	}

	return state.Attempt{
		Channel:   channel,
		RealModel: input.Attempt.RealModel,
		Mode:      input.Attempt.Mode,
		Source:    input.Attempt.Channel.Source,
		SourceID:  input.Attempt.Channel.ID,
	}, nil
}

func (f *channelFinder) findChannel(user *app.UserInfo, attempt attemptwire.BoundAttempt) (*models.Channel, error) {
	switch attempt.Channel.Source {
	case attemptwire.SourceAdmin:
		return f.findAdminChannel(user, attempt.Channel.ID, attempt.RealModel)
	case attemptwire.SourcePrivate:
		return f.findPrivateChannel(user, attempt.Channel.ID, attempt.RealModel)
	default:
		return nil, boundChannelError(boundSourceInvalid)
	}
}

func (f *channelFinder) findAdminChannel(user *app.UserInfo, channelID uint, realModel string) (*models.Channel, error) {
	if user.BYOKOnly {
		return nil, boundChannelError(boundChannelForbidden)
	}
	if f.cache == nil {
		return nil, boundChannelError(boundChannelNotFound)
	}

	for _, channel := range f.cache.GetChannelsForModel(realModel) {
		if !availableAdminChannel(channel, channelID, realModel) {
			continue
		}
		if !channelIDAllowed(channelID, user.GroupAllowedChannelIDs) ||
			!channelIDAllowed(channelID, user.AllowedChannelIDs) {
			return nil, boundChannelError(boundChannelForbidden)
		}
		return channel, nil
	}
	return nil, boundChannelError(boundChannelNotFound)
}

func (f *channelFinder) findPrivateChannel(user *app.UserInfo, channelID uint, realModel string) (*models.Channel, error) {
	if user.UserID == 0 {
		return nil, boundChannelError(boundChannelForbidden)
	}
	if f.cache == nil {
		return nil, boundChannelError(boundChannelNotFound)
	}

	for _, channel := range f.cache.GetVisiblePrivateChannelsForUser(user.UserID, realModel) {
		if channel == nil || channel.ID != channelID || channel.Status != consts.StatusEnabled ||
			!slices.Contains(channel.Models, realModel) {
			continue
		}
		return upstream.ProjectPrivateChannelToChannel(channel), nil
	}
	return nil, boundChannelError(boundChannelNotFound)
}

func availableAdminChannel(channel *models.Channel, channelID uint, realModel string) bool {
	if channel == nil || channel.ID != channelID || channel.Status != consts.StatusEnabled {
		return false
	}
	for model := range strings.SplitSeq(channel.Models, ",") {
		if strings.TrimSpace(model) == realModel {
			return true
		}
	}
	return false
}

func channelIDAllowed(channelID uint, allowed []uint) bool {
	return len(allowed) == 0 || slices.Contains(allowed, channelID)
}

func invalidAttemptError(attempt attemptwire.BoundAttempt) error {
	if attempt.Channel.ID == 0 || (attempt.Channel.Source != attemptwire.SourceAdmin && attempt.Channel.Source != attemptwire.SourcePrivate) {
		return boundChannelError(boundSourceInvalid)
	}
	if strings.TrimSpace(attempt.RealModel) == "" {
		return boundChannelError(boundModelForbidden)
	}
	return boundChannelError(boundModeMismatch)
}

func boundChannelError(code string) error {
	return &BoundChannelError{Code: code}
}
