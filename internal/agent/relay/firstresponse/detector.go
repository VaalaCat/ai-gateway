package firstresponse

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

const maxSSEFrameBytes = 64 * 1024

type sseDetector struct {
	tracker         *Tracker
	line            []byte
	frameBytes      int
	discardingFrame bool
	eventName       string
	dataLines       []string
}

func newSSEDetector(tracker *Tracker) *sseDetector {
	return &sseDetector{tracker: tracker}
}

func (detector *sseDetector) observe(chunk []byte, observedAt time.Time) {
	for _, currentByte := range chunk {
		detector.frameBytes++
		if detector.discardingFrame {
			detector.consumeDiscardedByte(currentByte)
			continue
		}

		if currentByte != '\n' {
			detector.line = append(detector.line, currentByte)
		}
		if detector.frameBytes > maxSSEFrameBytes {
			detector.discardOversizedFrame(currentByte)
			continue
		}
		if currentByte != '\n' {
			continue
		}

		line := bytes.TrimSuffix(detector.line, []byte{'\r'})
		detector.consumeLine(line, observedAt)
		detector.line = detector.line[:0]
	}
}

func (detector *sseDetector) consumeLine(line []byte, observedAt time.Time) {
	if len(line) == 0 {
		detector.finishFrame(observedAt)
		return
	}
	if line[0] == ':' {
		return
	}

	name, value := splitSSEField(line)
	switch name {
	case "event":
		detector.eventName = value
	case "data":
		detector.dataLines = append(detector.dataLines, value)
	}
}

func (detector *sseDetector) finishFrame(observedAt time.Time) {
	data := strings.Join(detector.dataLines, "\n")
	if (detector.eventName != "" || data != "") && !isHeartbeatEvent(detector.eventName, data) {
		detector.tracker.observeFirstEvent(observedAt)
	}
	detector.resetFrame()
}

func (detector *sseDetector) resetFrame() {
	detector.line = detector.line[:0]
	detector.frameBytes = 0
	detector.discardingFrame = false
	detector.eventName = ""
	clear(detector.dataLines)
	detector.dataLines = detector.dataLines[:0]
}

func (detector *sseDetector) discardOversizedFrame(currentByte byte) {
	blankLine := currentByte == '\n' && isBlankSSELine(detector.line)
	pendingCRLFBlankLine := currentByte == '\r' && isBlankSSELine(detector.line)
	detector.discardingFrame = true
	detector.eventName = ""
	clear(detector.dataLines)
	detector.dataLines = detector.dataLines[:0]
	detector.line = detector.line[:0]

	if currentByte == '\n' {
		if blankLine {
			detector.resetFrame()
		}
		return
	}
	if pendingCRLFBlankLine {
		detector.line = append(detector.line, '\r')
		return
	}

	// 两字节哨兵只标记当前丢弃行非空，使后续扫描到换行前仍保持有界内存。
	detector.line = append(detector.line, 0, 0)
}

func (detector *sseDetector) consumeDiscardedByte(currentByte byte) {
	if currentByte != '\n' {
		if len(detector.line) < 2 {
			detector.line = append(detector.line, currentByte)
		}
		return
	}

	blankLine := isBlankSSELine(detector.line)
	detector.line = detector.line[:0]
	if blankLine {
		detector.resetFrame()
	}
}

func isBlankSSELine(line []byte) bool {
	return len(line) == 0 || (len(line) == 1 && line[0] == '\r')
}

func splitSSEField(line []byte) (string, string) {
	name, value, found := bytes.Cut(line, []byte{':'})
	if !found {
		return string(name), ""
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return string(name), string(value)
}

func isHeartbeatEvent(eventName, data string) bool {
	isHeartbeatValue := func(value string) bool {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "ping", "keepalive":
			return true
		default:
			return false
		}
	}

	if isHeartbeatValue(eventName) || isHeartbeatValue(data) {
		return true
	}

	var payload struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return false
	}
	return isHeartbeatValue(payload.Type)
}
