package controlrpc

import "errors"

var (
	ErrDisconnected    = errors.New("control connection disconnected")
	ErrSendFailed      = errors.New("control RPC send failed")
	ErrInvalidResponse = errors.New("invalid agent response")
)
