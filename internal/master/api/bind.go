package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsontext"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

type BindMode int

const (
	BindNone BindMode = iota
	BindJSON
	BindOptionalJSON
	BindQuery
	BindURI
	BindURIAndJSON
	BindURIAndOptionalJSON
	BindURIAndQuery
	BindURIAndBodyMap
	BindStrictJSONText
	BindURIAndStrictJSONBodyMap
	BindURIAndStrictJSONText
)

var errInvalidJSONTextEncoding = errors.New("JSON body must use valid UTF-8 and Unicode surrogate pairs")

var _ RequestBinder = DefaultRequestBinder{}

type DefaultRequestBinder struct{}

func (DefaultRequestBinder) Bind(c *gin.Context, mode BindMode, req any) error {
	switch mode {
	case BindNone:
		return nil
	case BindJSON:
		return c.ShouldBindJSON(req)
	case BindOptionalJSON:
		if c.Request == nil || c.Request.Body == nil || c.Request.ContentLength == 0 {
			return nil
		}
		err := c.ShouldBindJSON(req)
		if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "invalid request") {
			return err
		}
		return nil
	case BindQuery:
		return c.ShouldBindQuery(req)
	case BindURI:
		return c.ShouldBindUri(req)
	case BindURIAndJSON:
		// 先解析 body（不触发校验），让来自 body 的 required 字段在 ShouldBindUri
		// 的整 struct 校验前就位；否则 ShouldBindUri 会因 body 字段未绑而误报 required。
		if c.Request != nil && c.Request.Body != nil {
			if err := json.NewDecoder(c.Request.Body).Decode(req); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
		}
		return c.ShouldBindUri(req) // 映射 URI + 对已填满的整 struct 只校验一次
	case BindURIAndOptionalJSON:
		if c.Request != nil && c.Request.Body != nil && c.Request.ContentLength != 0 {
			if err := json.NewDecoder(c.Request.Body).Decode(req); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
		}
		return c.ShouldBindUri(req)
	// 注意：BindURIAndQuery 同样先 ShouldBindUri 再 ShouldBindQuery，若某 req 在 query
	// 字段上标 binding:"required" 会有与 BindURIAndJSON 同类的早校验问题。当前所有
	// BindURIAndQuery 端点（ChannelDaily/TokenDaily）的 query 字段均非 required，故不改。
	case BindURIAndQuery:
		if err := c.ShouldBindUri(req); err != nil {
			return err
		}
		return c.ShouldBindQuery(req)
	case BindURIAndBodyMap:
		if err := c.ShouldBindUri(req); err != nil {
			return err
		}
		setter, ok := req.(BodyMapSetter)
		if !ok {
			return errors.New("request does not implement BodyMapSetter")
		}
		body := make(map[string]any)
		if err := c.ShouldBindJSON(&body); err != nil {
			return err
		}
		setter.SetBodyMap(body)
		return nil
	case BindStrictJSONText:
		return bindStrictJSONText(c, req)
	case BindURIAndStrictJSONBodyMap:
		if err := c.ShouldBindUri(req); err != nil {
			return err
		}
		setter, ok := req.(BodyMapSetter)
		if !ok {
			return errors.New("request does not implement BodyMapSetter")
		}
		body := make(map[string]any)
		if err := bindStrictJSONText(c, &body); err != nil {
			return err
		}
		setter.SetBodyMap(body)
		return nil
	case BindURIAndStrictJSONText:
		if err := decodeStrictJSONText(c, req); err != nil {
			return err
		}
		return c.ShouldBindUri(req)
	default:
		return errors.New("unknown bind mode")
	}
}

func decodeStrictJSONText(c *gin.Context, req any) error {
	if c.Request == nil || c.Request.Body == nil {
		return io.EOF
	}
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if !jsontext.ValidEncoding(payload) {
		return errInvalidJSONTextEncoding
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(req); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON body must contain exactly one value")
		}
		return err
	}
	return nil
}

func bindStrictJSONText(c *gin.Context, req any) error {
	if c.Request == nil || c.Request.Body == nil {
		return io.EOF
	}
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if !jsontext.ValidEncoding(payload) {
		return errInvalidJSONTextEncoding
	}
	return binding.JSON.BindBody(payload, req)
}
