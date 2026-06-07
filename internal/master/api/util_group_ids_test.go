package api_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

func TestValidateAllowedGroupIDs(t *testing.T) {
	if err := api.ValidateAllowedGroupIDs([]uint{1, 2, 3}); err != nil {
		t.Fatalf("valid ids errored: %v", err)
	}
	if err := api.ValidateAllowedGroupIDs([]uint{1, 0, 3}); err == nil {
		t.Fatal("expected error for zero id")
	}
	big := make([]uint, 101)
	for i := range big {
		big[i] = uint(i + 1)
	}
	if err := api.ValidateAllowedGroupIDs(big); err == nil {
		t.Fatal("expected error for >100 ids")
	}
}

func TestNormalizeAllowedGroupIDs(t *testing.T) {
	out, err := api.NormalizeAllowedGroupIDs([]any{float64(2), float64(5)})
	if err != nil {
		t.Fatalf("normalize valid: %v", err)
	}
	if len(out) != 2 || out[0] != 2 || out[1] != 5 {
		t.Fatalf("got %v", out)
	}
	if _, err := api.NormalizeAllowedGroupIDs(nil); err != nil {
		t.Fatalf("nil should be (nil,nil): %v", err)
	}
	if _, err := api.NormalizeAllowedGroupIDs("notarray"); err == nil {
		t.Fatal("expected error for non-array")
	}
	if _, err := api.NormalizeAllowedGroupIDs([]any{float64(-1)}); err == nil {
		t.Fatal("expected error for negative value")
	}
}
