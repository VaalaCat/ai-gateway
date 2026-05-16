package events

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type patternPayload struct {
	pattern string
	typ     reflect.Type
}

type defaultRegistry struct {
	exact    map[string]reflect.Type
	patterns []patternPayload
}

var DefaultRegistry TopicRegistry = NewRegistry()

func NewRegistry() TopicRegistry {
	tokenType := reflect.TypeOf(models.Token{})
	channelType := reflect.TypeOf(models.Channel{})
	modelType := reflect.TypeOf(models.ModelConfig{})
	agentType := reflect.TypeOf(models.Agent{})
	userType := reflect.TypeOf(models.User{})
	usageReportType := reflect.TypeOf(protocol.UsageReport{})
	usageEntryType := reflect.TypeOf(protocol.UsageLogEntry{})
	syncPushType := reflect.TypeOf(protocol.SyncPushParams{})
	emptyType := reflect.TypeOf(struct{}{})

	return &defaultRegistry{
		exact: map[string]reflect.Type{
			TokenCreateTopic.Value(): tokenType,
			TokenUpdateTopic.Value(): tokenType,
			TokenDeleteTopic.Value(): tokenType,

			ChannelCreateTopic.Value(): channelType,
			ChannelUpdateTopic.Value(): channelType,
			ChannelDeleteTopic.Value(): channelType,

			ModelCreateTopic.Value(): modelType,
			ModelUpdateTopic.Value(): modelType,
			ModelDeleteTopic.Value(): modelType,

			AgentRevokedTopic.Value():    agentType,
			AgentRegisteredTopic.Value(): agentType,

			UsageReportedTopic.Value():  usageReportType,
			UsageCompletedTopic.Value(): usageEntryType,

			UserQuotaDepletedTopic.Value(): userType,

			SyncFullSyncRequestedTopic.Value(): emptyType,
		},
		patterns: []patternPayload{
			{pattern: TokenAllPattern.Value(), typ: tokenType},
			{pattern: ChannelAllPattern.Value(), typ: channelType},
			{pattern: ModelAllPattern.Value(), typ: modelType},

			{pattern: SyncTokenAllPattern.Value(), typ: syncPushType},
			{pattern: SyncChannelAllPattern.Value(), typ: syncPushType},
			{pattern: SyncModelAllPattern.Value(), typ: syncPushType},
			{pattern: SyncModelConfigAllPattern.Value(), typ: syncPushType},
		},
	}
}

func (r *defaultRegistry) PayloadType(topic string) (reflect.Type, bool) {
	if typ, ok := r.exact[topic]; ok {
		return typ, true
	}
	for _, item := range r.patterns {
		if matchPattern(item.pattern, topic) {
			return item.typ, true
		}
	}
	return nil, false
}

func (r *defaultRegistry) Validate(topic string, payload any) error {
	expectedType, ok := r.PayloadType(topic)
	if !ok {
		return fmt.Errorf("unknown event topic: %s", topic)
	}

	if expectedType == reflect.TypeOf(struct{}{}) {
		if payload == nil {
			return nil
		}
		actualType := normalizeType(reflect.TypeOf(payload))
		if actualType == expectedType {
			return nil
		}
		return fmt.Errorf("topic %s expects empty payload, got %s", topic, actualType.String())
	}

	if payload == nil {
		return fmt.Errorf("topic %s expects %s payload, got nil", topic, expectedType.String())
	}

	actualType := normalizeType(reflect.TypeOf(payload))
	if actualType != expectedType {
		return fmt.Errorf("topic %s expects %s payload, got %s", topic, expectedType.String(), actualType.String())
	}

	return nil
}

func normalizeType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func matchPattern(pattern, topic string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == topic
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(topic, prefix)
}
