package firstresponse

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetectorAcceptsAnyCompleteNonHeartbeatEvent(t *testing.T) {
	validEvents := []string{
		"event: response.created\ndata: {\"type\":\"response.created\"}\n\n",
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		"event: response.output_item.added\ndata: {\"item\":{\"type\":\"function_call\"}}\n\n",
		"data: [DONE]\n\n",
		"data: {not-json}\n\n",
	}

	for _, frame := range validEvents {
		t.Run(frame, func(t *testing.T) {
			startedAt := time.Unix(100, 0)
			tracker := NewTracker(startedAt)
			detector := newSSEDetector(tracker)

			detector.observe([]byte(frame), startedAt.Add(120*time.Millisecond))

			require.Equal(t, 120, tracker.Milliseconds())
		})
	}
}

func TestDetectorIgnoresHeartbeatFrames(t *testing.T) {
	heartbeats := []string{
		": keepalive\n\n",
		"event: ping\ndata: ignored\n\n",
		"event: keepalive\ndata: ignored\n\n",
		"data: ping\n\n",
		"data: KEEPALIVE\n\n",
		"data: {\"type\":\"Ping\"}\n\n",
		"data: {\"type\":\"keepalive\"}\n\n",
	}

	for _, frame := range heartbeats {
		t.Run(frame, func(t *testing.T) {
			startedAt := time.Unix(100, 0)
			tracker := NewTracker(startedAt)
			detector := newSSEDetector(tracker)

			detector.observe([]byte(frame), startedAt.Add(40*time.Millisecond))
			require.Zero(t, tracker.Milliseconds())

			detector.observe(
				[]byte("event: response.created\ndata: {}\n\n"),
				startedAt.Add(120*time.Millisecond),
			)
			require.Equal(t, 120, tracker.Milliseconds())
		})
	}
}

func TestDetectorCompletesLFFrameAcrossWrites(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)

	detector.observe([]byte("event: response.created"), startedAt.Add(40*time.Millisecond))
	detector.observe([]byte("\ndata: {}"), startedAt.Add(80*time.Millisecond))
	detector.observe([]byte("\n\n"), startedAt.Add(120*time.Millisecond))

	require.Equal(t, 120, tracker.Milliseconds())
}

func TestDetectorCompletesCRLFWhenFinalSeparatorIsSplitAcrossWrites(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)

	detector.observe(
		[]byte("event: response.created\r\ndata: {}\r\n\r"),
		startedAt.Add(80*time.Millisecond),
	)
	require.Zero(t, tracker.Milliseconds())

	detector.observe([]byte("\n"), startedAt.Add(120*time.Millisecond))
	require.Equal(t, 120, tracker.Milliseconds())
}

func TestDetectorFindsEventAfterHeartbeatInSameWrite(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)

	detector.observe(
		[]byte(": keepalive\n\nevent: response.created\ndata: {}\n\n"),
		startedAt.Add(120*time.Millisecond),
	)

	require.Equal(t, 120, tracker.Milliseconds())
}

func TestDetectorIgnoresFramesWithoutEventOrData(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)

	detector.observe(
		[]byte(": comment\nid: 42\nretry: 1000\nunknown: value\n\n"),
		startedAt.Add(120*time.Millisecond),
	)

	require.Zero(t, tracker.Milliseconds())
}

func TestDetectorRecoversAfterOversizedFrameWithSplitTerminator(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)
	oversizedFrame := "data: " + strings.Repeat("x", maxSSEFrameBytes+1) + "\n\r"

	detector.observe([]byte(oversizedFrame), startedAt.Add(40*time.Millisecond))
	require.Zero(t, tracker.Milliseconds())
	require.LessOrEqual(t, len(detector.line), maxSSEFrameBytes)

	detector.observe([]byte("\n"), startedAt.Add(80*time.Millisecond))
	detector.observe(
		[]byte("event: response.created\ndata: {}\n\n"),
		startedAt.Add(120*time.Millisecond),
	)

	require.Equal(t, 120, tracker.Milliseconds())
}

func TestDetectorRecoversWhenOversizedFrameEndsAtCRLFBoundary(t *testing.T) {
	testCases := map[string]struct {
		chunks [][]byte
	}{
		"terminator in one write": {
			chunks: [][]byte{
				[]byte("data: " + strings.Repeat("x", maxSSEFrameBytes-8) + "\r\n\r\n"),
			},
		},
		"terminator across writes": {
			chunks: [][]byte{
				[]byte("data: " + strings.Repeat("x", maxSSEFrameBytes-8) + "\r\n\r"),
				[]byte("\n"),
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			startedAt := time.Unix(100, 0)
			tracker := NewTracker(startedAt)
			detector := newSSEDetector(tracker)

			for _, chunk := range testCase.chunks {
				detector.observe(chunk, startedAt.Add(40*time.Millisecond))
			}
			require.Zero(t, tracker.Milliseconds())

			detector.observe(
				[]byte("event: response.created\ndata: {}\n\n"),
				startedAt.Add(120*time.Millisecond),
			)

			require.Equal(t, 120, tracker.Milliseconds())
		})
	}
}

func TestDetectorReleasesCompletedFrameDataReferences(t *testing.T) {
	startedAt := time.Unix(100, 0)
	tracker := NewTracker(startedAt)
	detector := newSSEDetector(tracker)

	detector.observe(
		[]byte("data: retained first value\ndata: retained second value\n\n"),
		startedAt.Add(40*time.Millisecond),
	)
	detector.observe(
		[]byte("data: replacement value\n\n"),
		startedAt.Add(80*time.Millisecond),
	)

	for _, retainedValue := range detector.dataLines[:cap(detector.dataLines)] {
		require.Empty(t, retainedValue)
	}
}
