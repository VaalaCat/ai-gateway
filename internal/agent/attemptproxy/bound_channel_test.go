package attemptproxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type privateChannelQuery struct {
	userID uint
	model  string
}

type boundChannelCache struct {
	app.AgentCache

	adminChannels   []*models.Channel
	privateChannels []*protocol.SyncedPrivateChannel
	adminLookups    int
	privateLookups  int
	adminModels     []string
	privateQueries  []privateChannelQuery
}

func newBoundChannelCache(admin []*models.Channel, private []*protocol.SyncedPrivateChannel) *boundChannelCache {
	return &boundChannelCache{adminChannels: admin, privateChannels: private}
}

func (c *boundChannelCache) GetChannelsForModel(model string) []*models.Channel {
	c.adminLookups++
	c.adminModels = append(c.adminModels, model)
	return c.adminChannels
}

func (c *boundChannelCache) GetVisiblePrivateChannelsForUser(userID uint, model string) []*protocol.SyncedPrivateChannel {
	c.privateLookups++
	c.privateQueries = append(c.privateQueries, privateChannelQuery{userID: userID, model: model})
	return c.privateChannels
}

func TestBoundChannelFinderRestoresAdminAttempt(t *testing.T) {
	channel := adminChannel(7, "admin-secret", "gpt-4o")
	cache := newBoundChannelCache([]*models.Channel{channel}, nil)
	finder := NewBoundChannelFinder(cache)

	got, err := finder.Find(BoundChannelInput{
		User:            allowedUser(11),
		Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
		InboundProtocol: codec.ProtocolOpenAIChat,
	})

	require.NoError(t, err)
	require.Same(t, channel, got.Channel)
	require.Equal(t, "gpt-4o", got.RealModel)
	require.Equal(t, attemptwire.ModeNative, got.Mode)
	require.Equal(t, attemptwire.SourceAdmin, got.Source)
	require.Equal(t, uint(7), got.SourceID)
	require.Equal(t, []string{"gpt-4o"}, cache.adminModels)
	require.Equal(t, 1, cache.adminLookups)
	require.Zero(t, cache.privateLookups)
}

func TestBoundChannelFinderRestoresPrivateAttemptWithPlaintextKey(t *testing.T) {
	private := privateChannel(7, "private-secret", "gpt-4o")
	cache := newBoundChannelCache(nil, []*protocol.SyncedPrivateChannel{private})
	finder := NewBoundChannelFinder(cache)

	got, err := finder.Find(BoundChannelInput{
		User:            allowedUser(11),
		Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
		InboundProtocol: codec.ProtocolOpenAIChat,
	})

	require.NoError(t, err)
	require.Equal(t, uint(7), got.Channel.ID)
	require.Equal(t, "private-secret", got.Channel.Key)
	require.Equal(t, "gpt-4o", got.Channel.Models)
	require.Equal(t, "gpt-4o", got.RealModel)
	require.Equal(t, attemptwire.ModeNative, got.Mode)
	require.Equal(t, attemptwire.SourcePrivate, got.Source)
	require.Equal(t, uint(7), got.SourceID)
	require.Equal(t, []privateChannelQuery{{userID: 11, model: "gpt-4o"}}, cache.privateQueries)
	require.Zero(t, cache.adminLookups)
	require.Equal(t, 1, cache.privateLookups)
}

func TestBoundChannelFinderNeverCrossesSourceOnIDCollision(t *testing.T) {
	t.Run("private attempt only uses private lookup", func(t *testing.T) {
		cache := newBoundChannelCache(
			[]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")},
			[]*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")},
		)
		finder := NewBoundChannelFinder(cache)

		got, err := finder.Find(BoundChannelInput{
			User:            allowedUser(11),
			Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
			InboundProtocol: codec.ProtocolOpenAIChat,
		})

		require.NoError(t, err)
		require.Equal(t, "private-secret", got.Channel.Key)
		require.Zero(t, cache.adminLookups)
		require.Equal(t, 1, cache.privateLookups)
	})

	t.Run("admin attempt only uses admin lookup", func(t *testing.T) {
		cache := newBoundChannelCache(
			[]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")},
			[]*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")},
		)
		finder := NewBoundChannelFinder(cache)

		got, err := finder.Find(BoundChannelInput{
			User:            allowedUser(11),
			Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
			InboundProtocol: codec.ProtocolOpenAIChat,
		})

		require.NoError(t, err)
		require.Equal(t, "admin-secret", got.Channel.Key)
		require.Equal(t, 1, cache.adminLookups)
		require.Zero(t, cache.privateLookups)
	})
}

func TestBoundChannelFinderRejectsInvalidInputBeforeCacheLookup(t *testing.T) {
	valid := boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative)
	tests := []struct {
		name    string
		user    *app.UserInfo
		attempt attemptwire.BoundAttempt
		code    string
	}{
		{name: "nil user", user: nil, attempt: valid, code: "bound_channel_forbidden"},
		{name: "zero id", user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 0, "gpt-4o", attemptwire.ModeNative), code: "bound_source_invalid"},
		{name: "empty source", user: allowedUser(11), attempt: boundAttempt("", 7, "gpt-4o", attemptwire.ModeNative), code: "bound_source_invalid"},
		{name: "invalid source", user: allowedUser(11), attempt: boundAttempt("unknown", 7, "gpt-4o", attemptwire.ModeNative), code: "bound_source_invalid"},
		{name: "empty real model", user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 7, "", attemptwire.ModeNative), code: "bound_model_forbidden"},
		{name: "whitespace real model", user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 7, "   ", attemptwire.ModeNative), code: "bound_model_forbidden"},
		{name: "empty mode", user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", ""), code: "bound_mode_mismatch"},
		{name: "invalid mode", user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", "unknown"), code: "bound_mode_mismatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil)
			_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            tt.user,
				Attempt:         tt.attempt,
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			requireBoundChannelError(t, err, tt.code)
			require.Zero(t, cache.adminLookups)
			require.Zero(t, cache.privateLookups)
		})
	}
}

func TestBoundChannelFinderEnforcesTokenAndGroupModels(t *testing.T) {
	tests := []struct {
		name string
		user *app.UserInfo
		code string
	}{
		{
			name: "token models reject real model",
			user: &app.UserInfo{UserID: 11, TokenModels: []string{"claude-3-7-sonnet"}},
			code: "bound_model_forbidden",
		},
		{
			name: "group models reject real model",
			user: &app.UserInfo{UserID: 11, GroupModels: []string{"claude-3-7-sonnet"}},
			code: "bound_model_forbidden",
		},
		{
			name: "both model layers allow real model",
			user: &app.UserInfo{UserID: 11, TokenModels: []string{"gpt-.*"}, GroupModels: []string{"gpt-4o"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil)
			got, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            tt.user,
				Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			if tt.code != "" {
				requireBoundChannelError(t, err, tt.code)
				require.Zero(t, cache.adminLookups)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint(7), got.SourceID)
			require.Equal(t, 1, cache.adminLookups)
		})
	}
}

func TestBoundChannelFinderRejectsUnavailableAdminChannels(t *testing.T) {
	tests := []struct {
		name    string
		channel *models.Channel
	}{
		{
			name: "disabled",
			channel: func() *models.Channel {
				channel := adminChannel(7, "admin-secret", "gpt-4o")
				channel.Status = consts.StatusDisabled
				return channel
			}(),
		},
		{name: "model mismatch", channel: adminChannel(7, "admin-secret", "claude-3-7-sonnet")},
		{name: "bound id absent", channel: adminChannel(8, "admin-secret", "gpt-4o")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache([]*models.Channel{tt.channel}, nil)
			_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            allowedUser(11),
				Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			requireBoundChannelError(t, err, "bound_channel_not_found")
			require.Equal(t, 1, cache.adminLookups)
			require.Zero(t, cache.privateLookups)
		})
	}
}

func TestBoundChannelFinderEnforcesAdminChannelWhitelists(t *testing.T) {
	tests := []struct {
		name          string
		allowed       []uint
		groupAllowed  []uint
		wantErrorCode string
	}{
		{name: "both empty allow", allowed: nil, groupAllowed: nil},
		{name: "token whitelist misses", allowed: []uint{8}, groupAllowed: []uint{7}, wantErrorCode: "bound_channel_forbidden"},
		{name: "group whitelist misses", allowed: []uint{7}, groupAllowed: []uint{8}, wantErrorCode: "bound_channel_forbidden"},
		{name: "both contain id allow", allowed: []uint{7, 8}, groupAllowed: []uint{6, 7}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil)
			user := allowedUser(11)
			user.AllowedChannelIDs = tt.allowed
			user.GroupAllowedChannelIDs = tt.groupAllowed
			got, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            user,
				Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			if tt.wantErrorCode != "" {
				requireBoundChannelError(t, err, tt.wantErrorCode)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint(7), got.SourceID)
		})
	}
}

func TestBoundChannelFinderRejectsAdminForBYOKOnlyUser(t *testing.T) {
	cache := newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil)
	user := allowedUser(11)
	user.BYOKOnly = true

	_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
		User:            user,
		Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative),
		InboundProtocol: codec.ProtocolOpenAIChat,
	})

	requireBoundChannelError(t, err, "bound_channel_forbidden")
	require.Zero(t, cache.adminLookups)
	require.Zero(t, cache.privateLookups)
}

func TestBoundChannelFinderPrivateAuthorizationBoundaries(t *testing.T) {
	t.Run("admin channel whitelists do not restrict private", func(t *testing.T) {
		cache := newBoundChannelCache(nil, []*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")})
		user := &app.UserInfo{
			UserID:                 11,
			BYOKOnly:               true,
			TokenModels:            []string{"gpt-.*"},
			GroupModels:            []string{"gpt-4o"},
			AllowedChannelIDs:      []uint{100},
			GroupAllowedChannelIDs: []uint{200},
		}

		got, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
			User:            user,
			Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
			InboundProtocol: codec.ProtocolOpenAIChat,
		})

		require.NoError(t, err)
		require.Equal(t, "private-secret", got.Channel.Key)
		require.Zero(t, cache.adminLookups)
		require.Equal(t, 1, cache.privateLookups)
	})

	for _, tt := range []struct {
		name string
		user *app.UserInfo
	}{
		{name: "token models reject private", user: &app.UserInfo{UserID: 11, TokenModels: []string{"claude-3-7-sonnet"}}},
		{name: "group models reject private", user: &app.UserInfo{UserID: 11, GroupModels: []string{"claude-3-7-sonnet"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache(nil, []*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")})

			_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            tt.user,
				Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			requireBoundChannelError(t, err, "bound_model_forbidden")
			require.Zero(t, cache.adminLookups)
			require.Zero(t, cache.privateLookups)
		})
	}

	t.Run("zero user id is forbidden", func(t *testing.T) {
		cache := newBoundChannelCache(nil, []*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")})

		_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
			User:            allowedUser(0),
			Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
			InboundProtocol: codec.ProtocolOpenAIChat,
		})

		requireBoundChannelError(t, err, "bound_channel_forbidden")
		require.Zero(t, cache.adminLookups)
		require.Zero(t, cache.privateLookups)
	})

	tests := []struct {
		name     string
		channels []*protocol.SyncedPrivateChannel
	}{
		{name: "not visible", channels: nil},
		{
			name: "disabled dirty cache entry",
			channels: func() []*protocol.SyncedPrivateChannel {
				channel := privateChannel(7, "private-secret", "gpt-4o")
				channel.Status = consts.StatusDisabled
				return []*protocol.SyncedPrivateChannel{channel}
			}(),
		},
		{name: "model mismatch dirty cache entry", channels: []*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "claude-3-7-sonnet")}},
		{name: "bound id absent", channels: []*protocol.SyncedPrivateChannel{privateChannel(8, "private-secret", "gpt-4o")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newBoundChannelCache(nil, tt.channels)
			_, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            allowedUser(11),
				Attempt:         boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeNative),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			requireBoundChannelError(t, err, "bound_channel_not_found")
			require.Zero(t, cache.adminLookups)
			require.Equal(t, 1, cache.privateLookups)
		})
	}
}

func TestBoundChannelFinderRechecksExecutionMode(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*models.Channel)
		boundMode attemptwire.ExecutionMode
		wantCode  string
	}{
		{name: "native matches", configure: func(*models.Channel) {}, boundMode: attemptwire.ModeNative},
		{name: "passthrough matches", configure: func(channel *models.Channel) { channel.PassthroughEnabled = true }, boundMode: attemptwire.ModePassthrough},
		{name: "legacy matches", configure: func(channel *models.Channel) { channel.UseLegacyAdaptor = true }, boundMode: attemptwire.ModeLegacy},
		{name: "computed native rejects bound legacy", configure: func(*models.Channel) {}, boundMode: attemptwire.ModeLegacy, wantCode: "bound_mode_mismatch"},
		{name: "computed passthrough rejects bound native", configure: func(channel *models.Channel) { channel.PassthroughEnabled = true }, boundMode: attemptwire.ModeNative, wantCode: "bound_mode_mismatch"},
		{name: "computed legacy rejects bound passthrough", configure: func(channel *models.Channel) { channel.UseLegacyAdaptor = true }, boundMode: attemptwire.ModePassthrough, wantCode: "bound_mode_mismatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := adminChannel(7, "admin-secret", "gpt-4o")
			tt.configure(channel)
			cache := newBoundChannelCache([]*models.Channel{channel}, nil)

			got, err := NewBoundChannelFinder(cache).Find(BoundChannelInput{
				User:            allowedUser(11),
				Attempt:         boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", tt.boundMode),
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			if tt.wantCode != "" {
				requireBoundChannelError(t, err, tt.wantCode)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.boundMode, got.Mode)
		})
	}
}

func TestBoundChannelErrorCodesAreStableAndDoNotLeakSecrets(t *testing.T) {
	tests := []struct {
		name    string
		cache   *boundChannelCache
		user    *app.UserInfo
		attempt attemptwire.BoundAttempt
		code    string
	}{
		{
			name:  "invalid source",
			cache: newBoundChannelCache(nil, nil), user: allowedUser(11),
			attempt: boundAttempt("invalid", 7, "gpt-4o", attemptwire.ModeNative), code: "bound_source_invalid",
		},
		{
			name: "channel not found",
			cache: func() *boundChannelCache {
				channel := adminChannel(7, "admin-secret", "gpt-4o")
				channel.Status = consts.StatusDisabled
				return newBoundChannelCache([]*models.Channel{channel}, nil)
			}(),
			user: allowedUser(11), attempt: boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative), code: "bound_channel_not_found",
		},
		{
			name:  "channel forbidden",
			cache: newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil),
			user: func() *app.UserInfo {
				user := allowedUser(11)
				user.AllowedChannelIDs = []uint{8}
				return user
			}(),
			attempt: boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative), code: "bound_channel_forbidden",
		},
		{
			name:    "model forbidden",
			cache:   newBoundChannelCache([]*models.Channel{adminChannel(7, "admin-secret", "gpt-4o")}, nil),
			user:    &app.UserInfo{UserID: 11, TokenModels: []string{"claude-3-7-sonnet"}},
			attempt: boundAttempt(attemptwire.SourceAdmin, 7, "gpt-4o", attemptwire.ModeNative), code: "bound_model_forbidden",
		},
		{
			name:  "mode mismatch",
			cache: newBoundChannelCache(nil, []*protocol.SyncedPrivateChannel{privateChannel(7, "private-secret", "gpt-4o")}),
			user:  allowedUser(11), attempt: boundAttempt(attemptwire.SourcePrivate, 7, "gpt-4o", attemptwire.ModeLegacy), code: "bound_mode_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBoundChannelFinder(tt.cache).Find(BoundChannelInput{
				User:            tt.user,
				Attempt:         tt.attempt,
				InboundProtocol: codec.ProtocolOpenAIChat,
			})

			requireBoundChannelError(t, err, tt.code)
			require.NotContains(t, err.Error(), "admin-secret")
			require.NotContains(t, err.Error(), "private-secret")
		})
	}
}

func requireBoundChannelError(t *testing.T, err error, wantCode string) {
	t.Helper()
	require.Error(t, err)
	var boundErr *BoundChannelError
	require.True(t, errors.As(err, &boundErr))
	require.Equal(t, wantCode, boundErr.Code)
	require.Equal(t, wantCode, BoundChannelErrorCode(err))
	require.Equal(t, wantCode, err.Error())
}

func allowedUser(userID uint) *app.UserInfo {
	return &app.UserInfo{UserID: userID}
}

func boundAttempt(source attemptwire.ChannelSource, id uint, model string, mode attemptwire.ExecutionMode) attemptwire.BoundAttempt {
	return attemptwire.BoundAttempt{
		Channel:   attemptwire.ChannelRef{Source: source, ID: id},
		RealModel: model,
		Mode:      mode,
	}
}

func adminChannel(id uint, key, modelsCSV string) *models.Channel {
	return &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:     id,
			Type:   consts.ChannelTypeOpenAI,
			Status: consts.StatusEnabled,
		},
		Key:    key,
		Models: modelsCSV,
	}
}

func privateChannel(id uint, key, model string) *protocol.SyncedPrivateChannel {
	return &protocol.SyncedPrivateChannel{
		ChannelCore: models.ChannelCore{
			ID:     id,
			Type:   consts.ChannelTypeOpenAI,
			Status: consts.StatusEnabled,
		},
		OwnerID:      11,
		KeyPlaintext: key,
		Models:       []string{model},
	}
}
