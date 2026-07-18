package channelfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func Decode[T any](r io.Reader, expected Kind) (Envelope[T], error) {
	var envelope Envelope[T]
	if r == nil {
		return envelope, NewError("invalid_channel_file", errors.New("missing body"))
	}

	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return envelope, NewError("invalid_channel_file", err)
	}
	if len(data) > MaxFileBytes {
		return envelope, NewError("file_too_large", fmt.Errorf("maximum %d bytes", MaxFileBytes))
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, NewError("invalid_channel_file", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return envelope, NewError("invalid_channel_file", errors.New("trailing JSON value"))
	}
	if envelope.SchemaVersion != SchemaVersion {
		return envelope, NewError("unsupported_schema_version", fmt.Errorf("got %d", envelope.SchemaVersion))
	}
	if envelope.Kind != expected {
		return envelope, NewError("channel_file_kind_mismatch", fmt.Errorf("got %q, want %q", envelope.Kind, expected))
	}
	if envelope.Channels == nil {
		return envelope, NewError("invalid_channel_file", errors.New("channels must be an array"))
	}
	if len(envelope.Channels) > MaxChannels {
		return envelope, NewError("too_many_channels", fmt.Errorf("got %d, maximum %d", len(envelope.Channels), MaxChannels))
	}
	return envelope, nil
}

func Encode[T any](w io.Writer, envelope Envelope[T]) error {
	if w == nil {
		return errors.New("channel file: missing writer")
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}
