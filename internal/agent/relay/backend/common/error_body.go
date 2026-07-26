package common

import (
	"context"
	"errors"
	"io"
)

const (
	DefaultErrorBodyHeadLimit = 64 << 10
	DefaultErrorBodyTailLimit = 64 << 10
	DefaultErrorBodyMaxRead   = 1 << 20
	TruncatedBodyMarker       = "...(truncated)"
)

type ErrorBodyLimits struct {
	Head    int
	Tail    int
	MaxRead int64
}

type ErrorBodyCapture struct {
	Head      []byte
	Tail      []byte
	TotalSeen int64
	Truncated bool
}

func (capture ErrorBodyCapture) BoundedHead() []byte {
	body := append([]byte(nil), capture.Head...)
	if capture.TotalSeen > int64(len(capture.Head)) {
		body = append(body, TruncatedBodyMarker...)
	}
	return body
}

func DefaultErrorBodyLimits() ErrorBodyLimits {
	return ErrorBodyLimits{
		Head:    DefaultErrorBodyHeadLimit,
		Tail:    DefaultErrorBodyTailLimit,
		MaxRead: DefaultErrorBodyMaxRead,
	}
}

func ReadBoundedErrorBody(ctx context.Context, body io.ReadCloser, limits ErrorBodyLimits) (ErrorBodyCapture, error) {
	limits = normalizeErrorBodyLimits(limits)
	if ctx == nil {
		ctx = context.Background()
	}
	if body == nil {
		return ErrorBodyCapture{}, nil
	}
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClose()
	defer body.Close()

	capture := ErrorBodyCapture{Head: make([]byte, 0, limits.Head)}
	tail := NewMaskingTail(limits.Tail)
	buf := make([]byte, 32<<10)
	for capture.TotalSeen <= limits.MaxRead {
		remaining := limits.MaxRead + 1 - capture.TotalSeen
		readBuf := buf
		if int64(len(readBuf)) > remaining {
			readBuf = readBuf[:remaining]
		}
		n, readErr := body.Read(readBuf)
		if n > 0 {
			chunk := readBuf[:n]
			capture.TotalSeen += int64(n)
			appendErrorBodyHead(&capture, chunk, limits.Head)
			_, _ = tail.Write(chunk)
			if capture.TotalSeen > limits.MaxRead {
				capture.Truncated = true
				capture.Tail = append([]byte(nil), tail.Bytes()...)
				return capture, nil
			}
		}
		if readErr != nil {
			capture.Tail = append([]byte(nil), tail.Bytes()...)
			if ctxErr := context.Cause(ctx); ctxErr != nil {
				return capture, ctxErr
			}
			if errors.Is(readErr, io.EOF) {
				return capture, nil
			}
			return capture, readErr
		}
		if n == 0 {
			capture.Tail = append([]byte(nil), tail.Bytes()...)
			return capture, io.ErrNoProgress
		}
	}
	panic("unreachable")
}

func normalizeErrorBodyLimits(limits ErrorBodyLimits) ErrorBodyLimits {
	defaults := DefaultErrorBodyLimits()
	if limits.Head <= 0 {
		limits.Head = defaults.Head
	}
	if limits.Tail <= 0 {
		limits.Tail = defaults.Tail
	}
	if limits.MaxRead <= 0 {
		limits.MaxRead = defaults.MaxRead
	}
	if int64(limits.Head) > limits.MaxRead {
		limits.Head = int(limits.MaxRead)
	}
	if int64(limits.Tail) > limits.MaxRead {
		limits.Tail = int(limits.MaxRead)
	}
	return limits
}

func appendErrorBodyHead(capture *ErrorBodyCapture, chunk []byte, limit int) {
	remaining := limit - len(capture.Head)
	if remaining <= 0 {
		return
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	capture.Head = append(capture.Head, chunk...)
}
