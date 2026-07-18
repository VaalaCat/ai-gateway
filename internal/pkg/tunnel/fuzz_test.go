package tunnel

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func FuzzDecodeFrame(f *testing.F) {
	valid, err := Encode(Frame{
		Version:  1,
		Type:     FrameRequestData,
		StreamID: testStreamID(),
		Sequence: 0,
		Payload:  []byte("payload"),
	}, testLimits())
	if err != nil {
		f.Fatalf("encode seed: %v", err)
	}

	f.Add([]byte(nil))
	f.Add(make([]byte, HeaderSize-1))
	f.Add(valid)
	f.Add(append(append([]byte(nil), valid...), 0xff))
	f.Add(func() []byte {
		data := append([]byte(nil), valid...)
		binary.BigEndian.PutUint32(data[24:28], ^uint32(0))
		return data
	}())

	f.Fuzz(func(t *testing.T, data []byte) {
		frame, err := Decode(data, testLimits())
		if err != nil {
			return
		}
		if len(data) < HeaderSize {
			t.Fatalf("Decode succeeded with %d-byte header", len(data))
		}
		declared := binary.BigEndian.Uint32(data[24:28])
		actual := len(data) - HeaderSize
		if uint64(actual) != uint64(declared) {
			t.Fatalf("message payload length = %d, declared = %d", actual, declared)
		}
		if uint64(len(frame.Payload)) != uint64(declared) {
			t.Fatalf("payload length = %d, declared = %d", len(frame.Payload), declared)
		}
		if !bytes.Equal(frame.Payload, data[HeaderSize:]) {
			t.Fatalf("decoded payload differs from complete message payload")
		}
		if len(frame.Payload) > MaxV1PayloadBytes || cap(frame.Payload) > MaxV1PayloadBytes {
			t.Fatalf("payload allocation exceeds limit: len=%d cap=%d", len(frame.Payload), cap(frame.Payload))
		}
	})
}
