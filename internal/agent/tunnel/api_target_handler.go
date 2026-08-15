package tunnel

import "context"

// APITargetHandler owns execution of one committed Generic API tunnel stream.
// Session owns transport lifecycle; the handler owns request consumption and
// the unique Headers/Data/End/Result response sequence.
type APITargetHandler interface {
	ServeHTTPAPI(context.Context, *APITargetStream) error
}

type APITargetHandlerFunc func(context.Context, *APITargetStream) error

func (f APITargetHandlerFunc) ServeHTTPAPI(ctx context.Context, stream *APITargetStream) error {
	return f(ctx, stream)
}
