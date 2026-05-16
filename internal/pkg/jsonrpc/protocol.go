package jsonrpc

import "encoding/json"

const Version = "2.0"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *int64          `json:"id,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      *int64          `json:"id"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

func NewRequest(method string, params any, id *int64) (*Request, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return &Request{JSONRPC: Version, Method: method, Params: raw, ID: id}, nil
}

func NewNotification(method string, params any) (*Request, error) {
	return NewRequest(method, params, nil)
}

func NewResponse(id *int64, result any) (*Response, error) {
	b, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Response{JSONRPC: Version, Result: b, ID: id}, nil
}

func NewErrorResponse(id *int64, code int, message string) *Response {
	return &Response{JSONRPC: Version, Error: &Error{Code: code, Message: message}, ID: id}
}
