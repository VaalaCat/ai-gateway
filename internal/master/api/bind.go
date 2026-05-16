package api

import (
	"errors"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
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
)

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
		if err := c.ShouldBindUri(req); err != nil {
			return err
		}
		return c.ShouldBindJSON(req)
	case BindURIAndOptionalJSON:
		if err := c.ShouldBindUri(req); err != nil {
			return err
		}
		if c.Request == nil || c.Request.Body == nil || c.Request.ContentLength == 0 {
			return nil
		}
		err := c.ShouldBindJSON(req)
		if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "invalid request") {
			return err
		}
		return nil
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
	default:
		return errors.New("unknown bind mode")
	}
}
