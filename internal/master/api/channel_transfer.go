package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/master/channelfile"
	"github.com/gin-gonic/gin"
)

func ParseChannelImportDryRun(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, BadRequestError("dry_run must be true or false", err)
	}
	return value, nil
}

func MapChannelFileError(err error) error {
	var fileErr *channelfile.Error
	if errors.As(err, &fileErr) {
		return ErrorWithCode(http.StatusBadRequest, fileErr.Code, fileErr.Error(), nil)
	}
	return BadRequestError("invalid channel file", err)
}

func WriteMappedError(adapter *Adapter, c *gin.Context, err error) {
	status, body := adapter.ErrorMapper.Map(err)
	adapter.Writer.WriteJSON(c, status, body)
}

func SetAttachmentHeaders(c *gin.Context, filename string) {
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
}
