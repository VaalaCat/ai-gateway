package common

type skMaskState uint8

const (
	skMaskIdle skMaskState = iota
	skMaskLiteral
	skMaskToken
	skMaskSkip
)

type namedMaskState uint8

const (
	namedMaskIdle namedMaskState = iota
	namedMaskMatchingLiteral
	namedMaskAwaitSpace
	namedMaskSpaces
	namedMaskSkip
)

type namedMaskKind uint8

const (
	bearerMask namedMaskKind = iota
	keyMask
)

type skMaskStage struct {
	state        skMaskState
	literalIndex int
	tokenCount   int
	pending      [23]byte
	pendingLen   int
}

type namedMaskStage struct {
	state        namedMaskState
	literalIndex int
	spaces       []byte
	spaceStart   int
	spaceTotal   int64
}

// MaskingTail applies the same sk, Bearer, and Key replacements as the final
// regexp masker before retaining a bounded output tail.
// behavior change: secrets cannot cross the hard tail boundary.
type MaskingTail struct {
	buf       []byte
	start     int
	limit     int
	totalSeen int64

	sk     skMaskStage
	bearer namedMaskStage
	key    namedMaskStage
}

func NewMaskingTail(limit int) *MaskingTail {
	if limit <= 0 {
		limit = DefaultErrorBodyTailLimit
	}
	return &MaskingTail{limit: limit}
}

func (capture *MaskingTail) Write(p []byte) (int, error) {
	for _, b := range p {
		capture.totalSeen++
		capture.consumeSK(b)
	}
	return len(p), nil
}

func (capture *MaskingTail) WriteString(s string) (int, error) {
	return capture.Write([]byte(s))
}

func (capture *MaskingTail) consumeSK(b byte) {
	const literal = "sk-"
	for {
		switch capture.sk.state {
		case skMaskIdle:
			if b == literal[0] {
				capture.sk.state = skMaskLiteral
				capture.sk.literalIndex = 1
				capture.sk.pending[0] = b
				capture.sk.pendingLen = 1
				return
			}
			capture.consumeBearer(b)
			return
		case skMaskLiteral:
			if b == literal[capture.sk.literalIndex] {
				capture.appendSKPending(b)
				capture.sk.literalIndex++
				if capture.sk.literalIndex == len(literal) {
					capture.sk.state = skMaskToken
				}
				return
			}
			capture.flushSKPending()
		case skMaskToken:
			if isASCIIAlphaNumeric(b) {
				capture.appendSKPending(b)
				capture.sk.tokenCount++
				if capture.sk.tokenCount == 20 {
					capture.clearSKPending()
					capture.emitToBearer("sk-***")
					capture.sk.state = skMaskSkip
				}
				return
			}
			capture.flushSKPending()
		case skMaskSkip:
			if isASCIIAlphaNumeric(b) {
				return
			}
			capture.resetSK()
		}
	}
}

func (capture *MaskingTail) appendSKPending(b byte) {
	capture.sk.pending[capture.sk.pendingLen] = b
	capture.sk.pendingLen++
}

func (capture *MaskingTail) flushSKPending() {
	for index := 0; index < capture.sk.pendingLen; index++ {
		capture.consumeBearer(capture.sk.pending[index])
	}
	capture.resetSK()
}

func (capture *MaskingTail) clearSKPending() {
	capture.sk.literalIndex = 0
	capture.sk.tokenCount = 0
	capture.sk.pendingLen = 0
}

func (capture *MaskingTail) resetSK() {
	capture.sk.state = skMaskIdle
	capture.clearSKPending()
}

func (capture *MaskingTail) emitToBearer(s string) {
	for index := 0; index < len(s); index++ {
		capture.consumeBearer(s[index])
	}
}

func (capture *MaskingTail) consumeBearer(b byte) {
	capture.consumeNamed(&capture.bearer, bearerMask, b)
}

func (capture *MaskingTail) consumeKey(b byte) {
	capture.consumeNamed(&capture.key, keyMask, b)
}

func (capture *MaskingTail) consumeNamed(stage *namedMaskStage, kind namedMaskKind, b byte) {
	literal := namedMaskLiteral(kind)
	for {
		switch stage.state {
		case namedMaskIdle:
			if b == literal[0] {
				stage.state = namedMaskMatchingLiteral
				stage.literalIndex = 1
				return
			}
			capture.emitNamedByte(kind, b)
			return
		case namedMaskMatchingLiteral:
			if b == literal[stage.literalIndex] {
				stage.literalIndex++
				if stage.literalIndex == len(literal) {
					stage.state = namedMaskAwaitSpace
				}
				return
			}
			capture.flushNamedPending(stage, kind)
		case namedMaskAwaitSpace:
			if isMaskSpace(b) {
				stage.state = namedMaskSpaces
				stage.appendSpace(b, capture.limit)
				return
			}
			capture.flushNamedPending(stage, kind)
		case namedMaskSpaces:
			if isMaskSpace(b) {
				stage.appendSpace(b, capture.limit)
				return
			}
			if isMaskTerminator(b) {
				capture.flushNamedPending(stage, kind)
				continue
			}
			stage.clearPending()
			capture.emitNamedString(kind, namedMaskReplacement(kind))
			stage.state = namedMaskSkip
			return
		case namedMaskSkip:
			if !isMaskTerminator(b) {
				return
			}
			stage.reset()
		}
	}
}

func (stage *namedMaskStage) appendSpace(b byte, limit int) {
	stage.spaceTotal++
	if len(stage.spaces) < limit {
		stage.spaces = append(stage.spaces, b)
		return
	}
	stage.spaces[stage.spaceStart] = b
	stage.spaceStart = (stage.spaceStart + 1) % limit
}

func (capture *MaskingTail) flushNamedPending(stage *namedMaskStage, kind namedMaskKind) {
	literal := namedMaskLiteral(kind)
	switch stage.state {
	case namedMaskMatchingLiteral:
		capture.emitNamedString(kind, literal[:stage.literalIndex])
	case namedMaskAwaitSpace:
		capture.emitNamedString(kind, literal)
	case namedMaskSpaces:
		if stage.spaceTotal <= int64(len(stage.spaces)) {
			capture.emitNamedString(kind, literal)
		}
		for index := 0; index < len(stage.spaces); index++ {
			spaceIndex := (stage.spaceStart + index) % len(stage.spaces)
			capture.emitNamedByte(kind, stage.spaces[spaceIndex])
		}
	}
	stage.reset()
}

func (stage *namedMaskStage) clearPending() {
	stage.literalIndex = 0
	stage.spaces = stage.spaces[:0]
	stage.spaceStart = 0
	stage.spaceTotal = 0
}

func (stage *namedMaskStage) reset() {
	stage.state = namedMaskIdle
	stage.clearPending()
}

func (capture *MaskingTail) emitNamedString(kind namedMaskKind, s string) {
	for index := 0; index < len(s); index++ {
		capture.emitNamedByte(kind, s[index])
	}
}

func (capture *MaskingTail) emitNamedByte(kind namedMaskKind, b byte) {
	if kind == bearerMask {
		capture.consumeKey(b)
		return
	}
	capture.appendByte(b)
}

func namedMaskLiteral(kind namedMaskKind) string {
	if kind == bearerMask {
		return "Bearer"
	}
	return "Key"
}

func namedMaskReplacement(kind namedMaskKind) string {
	if kind == bearerMask {
		return "Bearer ***"
	}
	return "Key ***"
}

func (capture *MaskingTail) appendByte(b byte) {
	if len(capture.buf) < capture.limit {
		capture.buf = append(capture.buf, b)
		return
	}
	capture.buf[capture.start] = b
	capture.start = (capture.start + 1) % capture.limit
}

func (capture *MaskingTail) Bytes() []byte {
	snapshot := capture.clone()
	snapshot.flushSKPending()
	snapshot.flushNamedPending(&snapshot.bearer, bearerMask)
	snapshot.flushNamedPending(&snapshot.key, keyMask)
	return snapshot.orderedBytes()
}

func (capture *MaskingTail) clone() *MaskingTail {
	snapshot := *capture
	snapshot.buf = append([]byte(nil), capture.buf...)
	snapshot.bearer.spaces = append([]byte(nil), capture.bearer.spaces...)
	snapshot.key.spaces = append([]byte(nil), capture.key.spaces...)
	return &snapshot
}

func (capture *MaskingTail) orderedBytes() []byte {
	if capture.start == 0 {
		return capture.buf
	}
	ordered := make([]byte, 0, len(capture.buf))
	ordered = append(ordered, capture.buf[capture.start:]...)
	return append(ordered, capture.buf[:capture.start]...)
}

func (capture *MaskingTail) String() string   { return string(capture.Bytes()) }
func (capture *MaskingTail) Len() int         { return len(capture.Bytes()) }
func (capture *MaskingTail) Limit() int       { return capture.limit }
func (capture *MaskingTail) TotalSeen() int64 { return capture.totalSeen }
func (capture *MaskingTail) Truncated() bool  { return capture.totalSeen > int64(capture.limit) }

func (capture *MaskingTail) SetTotalSeenLowerBound(total int64) {
	if total > capture.totalSeen {
		capture.totalSeen = total
	}
}

func (capture *MaskingTail) Reset() {
	capture.buf = capture.buf[:0]
	capture.start = 0
	capture.totalSeen = 0
	capture.resetSK()
	capture.bearer.reset()
	capture.key.reset()
}

func isMaskSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

func isMaskTerminator(b byte) bool {
	return isMaskSpace(b) || b == ',' || b == ';' || b == '"'
}

func isASCIIAlphaNumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
