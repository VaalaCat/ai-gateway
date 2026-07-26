package attemptproxy

import (
	"context"
	"reflect"
)

type AttemptResultWriter interface {
	WriteAttemptResult(AttemptProxyResult) error
}

type attemptResultWriterKey struct{}

func WithAttemptResultWriter(ctx context.Context, writer AttemptResultWriter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if isNilAttemptResultWriter(writer) {
		return ctx
	}
	return context.WithValue(ctx, attemptResultWriterKey{}, writer)
}

func AttemptResultWriterFromContext(ctx context.Context) (AttemptResultWriter, bool) {
	if ctx == nil {
		return nil, false
	}
	writer, ok := ctx.Value(attemptResultWriterKey{}).(AttemptResultWriter)
	return writer, ok && !isNilAttemptResultWriter(writer)
}

func isNilAttemptResultWriter(writer AttemptResultWriter) bool {
	if writer == nil {
		return true
	}
	value := reflect.ValueOf(writer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
