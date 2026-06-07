package ws

import "errors"

// ErrConnClosed 表示 ws 连接已关闭,所有在飞 Call 被就地终止。
var ErrConnClosed = errors.New("ws: connection closed")
