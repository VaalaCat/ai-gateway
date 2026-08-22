package llmkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/transport"
)

const (
	upstreamErrorBodyMaxRead = 4 * 1024
	upstreamErrorCauseMaxLen = 512
	truncatedCauseMarker     = " [truncated]"
)

type Client interface {
	Call(context.Context, Request, Target, CallOptions) (<-chan Event, error)
}

type clientImpl struct {
	codec      Codec
	httpClient HTTPDoer
}

func NewClient(options ClientOptions) Client {
	codec := options.Codec
	if codec == nil {
		codec = NewCodec()
	}
	doer := options.HTTPClient
	if doer == nil {
		doer = newDefaultHTTPClient()
	}
	return &clientImpl{codec: codec, httpClient: doer}
}

func (client *clientImpl) Call(
	ctx context.Context,
	request Request,
	target Target,
	options CallOptions,
) (<-chan Event, error) {
	attempt := cloneRequest(request)
	attempt.Model = target.Model
	encoded, err := client.codec.EncodeRequest(EncodeRequestInput{
		Request: attempt,
		Target:  target,
		Options: options.Conversion,
	})
	if err != nil {
		return nil, &Error{
			Stage: ErrorStageEncode, Cause: redactErrorCause(err, target.APIKey), redact: target.APIKey,
		}
	}

	httpRequest, err := transport.NewRequest(
		ctx, target.BaseURL, encoded.Method, encoded.Path, encoded.Headers, encoded.Body,
	)
	if err != nil {
		return nil, &Error{
			Stage: ErrorStageEncode, Cause: redactErrorCause(err, target.APIKey), redact: target.APIKey,
		}
	}
	doer := client.httpClient
	if options.HTTPClient != nil {
		doer = options.HTTPClient
	}
	response, err := doer.Do(httpRequest)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if clientError, ok := err.(*Error); ok {
			clientError.addRedaction(target.APIKey)
			return nil, clientError
		}
		return nil, &Error{
			Stage: ErrorStageConnect, Retryable: !isContextError(err),
			Cause: redactErrorCause(err, target.APIKey), redact: target.APIKey,
		}
	}
	if response == nil {
		return nil, &Error{Stage: ErrorStageConnect, Retryable: true, Cause: errNilHTTPResponse}
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		cause := readUpstreamErrorCause(ctx, response.Body, target.APIKey)
		return nil, &Error{
			Stage: ErrorStageUpstream, StatusCode: response.StatusCode,
			Retryable: retryableUpstreamError(response.StatusCode, cause),
			Cause:     redactErrorCause(cause, target.APIKey), redact: target.APIKey,
		}
	}

	events, err := client.codec.DecodeResponse(ctx, DecodeResponseInput{
		Protocol: target.Protocol, StatusCode: response.StatusCode, Headers: response.Header,
		Body: response.Body, Stream: attempt.Stream, State: encoded.State,
	})
	if err != nil {
		_ = response.Body.Close()
		return nil, &Error{
			Stage: ErrorStageDecode, Cause: redactErrorCause(err, target.APIKey), redact: target.APIKey,
		}
	}
	return eventsWithBodyLifecycle(ctx, events, response.Body), nil
}

func newDefaultHTTPClient() *http.Client {
	client := *http.DefaultClient
	client.CheckRedirect = checkSameOriginRedirect
	return &client
}

func checkSameOriginRedirect(request *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	origin := via[0].URL
	if !strings.EqualFold(request.URL.Scheme, origin.Scheme) ||
		!strings.EqualFold(request.URL.Host, origin.Host) {
		return fmt.Errorf("refusing cross-origin redirect from %s://%s to %s://%s",
			origin.Scheme, origin.Host, request.URL.Scheme, request.URL.Host)
	}
	return nil
}

func readUpstreamErrorCause(ctx context.Context, body io.ReadCloser, redact string) error {
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClose()
	defer body.Close()

	bounded, err := io.ReadAll(io.LimitReader(body, upstreamErrorBodyMaxRead+1))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("read upstream error response: %w", ctxErr)
	}
	if err != nil {
		return redactErrorCause(fmt.Errorf("read upstream error response: %w", err), redact)
	}
	truncated := len(bounded) > upstreamErrorBodyMaxRead
	if truncated {
		bounded = bounded[:upstreamErrorBodyMaxRead]
	}
	if len(strings.TrimSpace(string(bounded))) == 0 {
		return nil
	}

	message, errorType := parseProviderError(bounded)
	if message == "" && errorType == "" {
		message = strings.Join(strings.Fields(string(bounded)), " ")
	}
	if message == "" {
		message = errorType
	} else if errorType != "" {
		message = errorType + ": " + message
	}
	message = redactText(message, redact)
	message = truncateCause(message, truncated)
	if message == "" {
		return nil
	}
	return errors.New(message)
}

type redactedErrorCause struct {
	message string
	cause   error
}

func (cause *redactedErrorCause) Error() string { return cause.message }
func (cause *redactedErrorCause) Is(target error) bool {
	return errors.Is(cause.cause, target)
}

func redactErrorCause(cause error, value string) error {
	if cause == nil || value == "" || !strings.Contains(cause.Error(), value) {
		return cause
	}
	return &redactedErrorCause{message: redactText(cause.Error(), value), cause: cause}
}

func redactText(message string, value string) string {
	if value == "" {
		return message
	}
	return strings.ReplaceAll(message, value, "[REDACTED]")
}

func parseProviderError(body []byte) (message string, errorType string) {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", ""
	}
	if envelope.Error.Message != "" || envelope.Error.Type != "" {
		return envelope.Error.Message, envelope.Error.Type
	}
	return envelope.Message, envelope.Type
}

func truncateCause(message string, alreadyTruncated bool) string {
	message = strings.ToValidUTF8(message, "")
	if len(message) > upstreamErrorCauseMaxLen {
		cutoff := upstreamErrorCauseMaxLen
		for cutoff > 0 && !utf8.ValidString(message[:cutoff]) {
			cutoff--
		}
		message = message[:cutoff]
		alreadyTruncated = true
	}
	message = strings.TrimSpace(message)
	if alreadyTruncated {
		message += truncatedCauseMarker
	}
	return message
}

func retryableUpstreamError(statusCode int, cause error) bool {
	if isContextError(cause) {
		return false
	}
	return statusCode == http.StatusTooManyRequests ||
		(statusCode >= http.StatusInternalServerError && statusCode <= 599)
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func eventsWithBodyLifecycle(ctx context.Context, events <-chan Event, body interface{ Close() error }) <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		defer body.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
				if event.Type == EventDone || event.Type == EventError {
					go drainClientEvents(events)
					return
				}
			}
		}
	}()
	return out
}

func drainClientEvents(events <-chan Event) {
	for range events {
	}
}

func cloneRequest(request Request) Request {
	cloned := request
	cloned.Messages = cloneMessages(request.Messages)
	cloned.Tools = cloneTools(request.Tools)
	cloned.StopWords = append([]string(nil), request.StopWords...)
	cloned.Metadata = cloneStringAnyMap(request.Metadata)
	cloned.LogitBias = cloneStringIntMap(request.LogitBias)
	cloned.StreamOptions = cloneStringAnyMap(request.StreamOptions)
	cloned.Extras = cloneStringAnyMap(request.Extras)
	cloned.ResponseFormat = cloneAny(request.ResponseFormat)
	if request.ToolChoice != nil {
		value := *request.ToolChoice
		cloned.ToolChoice = &value
	}
	cloned.Temperature = clonePointer(request.Temperature)
	cloned.TopP = clonePointer(request.TopP)
	cloned.ParallelToolCalls = clonePointer(request.ParallelToolCalls)
	cloned.Store = clonePointer(request.Store)
	cloned.FrequencyPenalty = clonePointer(request.FrequencyPenalty)
	cloned.PresencePenalty = clonePointer(request.PresencePenalty)
	cloned.Seed = clonePointer(request.Seed)
	cloned.TopK = clonePointer(request.TopK)
	cloned.Logprobs = clonePointer(request.Logprobs)
	cloned.TopLogprobs = clonePointer(request.TopLogprobs)
	return cloned
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for index, message := range messages {
		cloned[index] = message
		if message.Content != nil {
			cloned[index].Content = make([]ContentBlock, len(message.Content))
			for contentIndex, content := range message.Content {
				cloned[index].Content[contentIndex] = content
				cloned[index].Content[contentIndex].Metadata = cloneStringAnyMap(content.Metadata)
				cloned[index].Content[contentIndex].RawJSON = append([]byte(nil), content.RawJSON...)
			}
		}
		cloned[index].ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
		cloned[index].RawJSON = append([]byte(nil), message.RawJSON...)
	}
	return cloned
}

func cloneTools(tools []Tool) []Tool {
	if tools == nil {
		return nil
	}
	cloned := make([]Tool, len(tools))
	for index, tool := range tools {
		cloned[index] = tool
		cloned[index].InputSchema = cloneAny(tool.InputSchema)
		cloned[index].RawConfig = cloneAny(tool.RawConfig)
		cloned[index].Strict = clonePointer(tool.Strict)
	}
	return cloned
}

func cloneStringAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneStringIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneAny(value any) any {
	if value == nil {
		return nil
	}
	return cloneMutableValue(reflect.ValueOf(value)).Interface()
}

func cloneMutableValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneMutableValue(value.Elem()))
		return cloned
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(cloneMutableValue(value.Elem()))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			cloned.SetMapIndex(iterator.Key(), cloneMutableValue(iterator.Value()))
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(cloneMutableValue(value.Index(index)))
		}
		return cloned
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(cloneMutableValue(value.Index(index)))
		}
		return cloned
	case reflect.Struct:
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(value)
		for index := 0; index < value.NumField(); index++ {
			if cloned.Field(index).CanSet() && value.Field(index).CanInterface() {
				cloned.Field(index).Set(cloneMutableValue(value.Field(index)))
			}
		}
		return cloned
	default:
		return value
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
