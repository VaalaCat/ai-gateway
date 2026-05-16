package billing

import (
	"fmt"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

type ListTokensRequest struct {
	api.PaginationQuery
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	TokenID   string `form:"token_id"`
	UserID    string `form:"user_id"`
}

type TokenDailyRequest struct {
	TokenID   string `uri:"token_id" binding:"required"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	UserID    string `form:"user_id"`
}

type OverviewRequest struct {
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	UserID    string `form:"user_id"`
}

type TokenDailyResponse struct {
	Items []dao.TokenBillingDailyItem `json:"items"`
}

func parseOptionalUint(raw string) (*uint, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil, err
	}
	parsed := uint(value)
	return &parsed, nil
}

func parseRequiredUint(raw string) (uint, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(value), nil
}

func normalizeDateRange(startDate, endDate string) (string, string, error) {
	start, err := normalizeDate(startDate)
	if err != nil {
		return "", "", err
	}
	end, err := normalizeDate(endDate)
	if err != nil {
		return "", "", err
	}
	if start != "" && end != "" && start > end {
		return "", "", fmt.Errorf("start_date %s is after end_date %s", start, end)
	}
	return start, end, nil
}

func normalizeDate(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", err
	}
	return parsed.Format("2006-01-02"), nil
}
