package agentproxy

import "github.com/VaalaCat/ai-gateway/internal/pkg/app"

type ProbePolicy = app.ProbePolicy
type AttemptStreamRequest = app.AttemptStreamRequest
type ProbeStreamRequest = app.ProbeStreamRequest
type AttemptStream = app.AttemptStream
type ProbeStream = app.ProbeStream
type AttemptStreamOpener = app.AttemptStreamOpener
type ProbeStreamOpener = app.ProbeStreamOpener
type RelayLink = app.RelayLink

const (
	ProbeRespectBusinessPolicy = app.ProbeRespectBusinessPolicy
	ProbeBypassBusinessPolicy  = app.ProbeBypassBusinessPolicy
)
