package dao

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestObsRange_Days_Success(t *testing.T) {
	r := ObsRange{Start: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix(),
		End: time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC).Unix()}
	require.Equal(t, 7, r.Days())
}

func TestObsRange_Validate_HourLimit(t *testing.T) {
	now := time.Now().UTC()
	r := ObsRange{
		Start: now.Add(-8 * 24 * time.Hour).Unix(),
		End:   now.Unix(),
		Gran:  GranHour,
	}
	require.ErrorIs(t, r.Validate(), ErrRangeOutOfBounds)
}

func TestObsRange_Validate_DayLimit(t *testing.T) {
	now := time.Now().UTC()
	r := ObsRange{
		Start: now.Add(-400 * 24 * time.Hour).Unix(),
		End:   now.Unix(),
		Gran:  GranDay,
	}
	require.ErrorIs(t, r.Validate(), ErrRangeOutOfBounds)
}

func TestObsRange_Validate_Boundary(t *testing.T) {
	now := time.Now().UTC()
	r := ObsRange{
		Start: now.Add(-7 * 24 * time.Hour).Unix(),
		End:   now.Unix(),
		Gran:  GranHour,
	}
	require.NoError(t, r.Validate(), "正好 7 天应通过 hour 上限")
}
