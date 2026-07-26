package channelfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func Decode[T any](r io.Reader, expected Kind) (Envelope[T], error) {
	data, err := readChannelFile(r)
	if err != nil {
		return Envelope[T]{}, err
	}
	return decodeEnvelope[T](data, expected)
}

func DecodeAdminChannelImport(r io.Reader) (Envelope[AdminChannel], error) {
	return decodeChannelImport[AdminChannel](r, KindAdminChannels)
}

func DecodeBYOKChannelImport(r io.Reader) (Envelope[BYOKChannel], error) {
	return decodeChannelImport[BYOKChannel](r, KindBYOKChannels)
}

func decodeChannelImport[To any](r io.Reader, target Kind) (Envelope[To], error) {
	data, err := readChannelFile(r)
	if err != nil {
		return Envelope[To]{}, err
	}
	metadata, err := decodeChannelMetadata(data)
	if err != nil {
		return Envelope[To]{}, err
	}
	if metadata.SchemaVersion != SchemaVersion {
		return Envelope[To]{}, NewError(
			"unsupported_schema_version",
			fmt.Errorf("got %d", metadata.SchemaVersion),
		)
	}
	source := metadata.Kind
	switch source {
	case KindAdminChannels:
		envelope, err := decodeEnvelope[AdminChannel](data, source)
		if err != nil {
			return Envelope[To]{}, err
		}
		return projectEnvelope[AdminChannel, To](envelope, target)
	case KindBYOKChannels:
		envelope, err := decodeEnvelope[BYOKChannel](data, source)
		if err != nil {
			return Envelope[To]{}, err
		}
		return projectEnvelope[BYOKChannel, To](envelope, target)
	default:
		return Envelope[To]{}, NewError(
			"channel_file_kind_mismatch",
			fmt.Errorf("got %q, want %q or %q", source, KindAdminChannels, KindBYOKChannels),
		)
	}
}

func readChannelFile(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, NewError("invalid_channel_file", errors.New("missing body"))
	}

	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, NewError("invalid_channel_file", err)
	}
	if len(data) > MaxFileBytes {
		return nil, NewError("file_too_large", fmt.Errorf("maximum %d bytes", MaxFileBytes))
	}
	return data, nil
}

type channelMetadata struct {
	SchemaVersion int  `json:"schema_version"`
	Kind          Kind `json:"kind"`
}

func decodeChannelMetadata(data []byte) (channelMetadata, error) {
	var metadata channelMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return channelMetadata{}, NewError("invalid_channel_file", err)
	}
	return metadata, nil
}

func decodeEnvelope[T any](data []byte, expected Kind) (Envelope[T], error) {
	var envelope Envelope[T]

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

func projectEnvelope[From, To any](source Envelope[From], target Kind) (Envelope[To], error) {
	data, err := json.Marshal(source.Channels)
	if err != nil {
		return Envelope[To]{}, NewError("invalid_channel_file", err)
	}
	channels := make([]To, 0, len(source.Channels))
	if err := json.Unmarshal(data, &channels); err != nil {
		return Envelope[To]{}, NewError("invalid_channel_file", err)
	}
	return Envelope[To]{
		SchemaVersion: source.SchemaVersion,
		Kind:          target,
		ExportedAt:    source.ExportedAt,
		Channels:      channels,
	}, nil
}

func Encode[T any](w io.Writer, envelope Envelope[T]) error {
	if w == nil {
		return errors.New("channel file: missing writer")
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}
