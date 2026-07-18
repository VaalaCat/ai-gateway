package dao_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type fakeNames struct {
	models   map[string]bool
	tokens   map[uint]bool
	routings map[string]*models.ModelRouting
}

func (f *fakeNames) HasModel(n string) bool                         { return f.models[n] }
func (f *fakeNames) HasToken(id uint) bool                          { return f.tokens[id] }
func (f *fakeNames) GetGlobalRouting(n string) *models.ModelRouting { return f.routings[n] }
func (f *fakeNames) AllGlobalRoutings() []*models.ModelRouting {
	out := make([]*models.ModelRouting, 0, len(f.routings))
	for _, r := range f.routings {
		out = append(out, r)
	}
	return out
}

func TestValidate_NameRules(t *testing.T) {
	nv := &fakeNames{models: map[string]bool{"gpt-4o": true}}
	cases := []struct {
		name     string
		in       *models.ModelRouting
		wantCode string
	}{
		{"empty name", &models.ModelRouting{Name: "", Scope: models.RoutingScopeGlobal, Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`}, "name_required"},
		{"name with comma", &models.ModelRouting{Name: "a,b", Scope: models.RoutingScopeGlobal, Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`}, "name_contains_comma"},
		{"too long", &models.ModelRouting{Name: string(make([]byte, 129)), Scope: models.RoutingScopeGlobal, Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`}, "name_too_long"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := dao.ValidateRouting(c.in, nv)
			if err == nil || err.Code() != c.wantCode {
				t.Errorf("want code %s, got %v", c.wantCode, err)
			}
		})
	}
}

func TestValidate_ScopeOwnerRules(t *testing.T) {
	nv := &fakeNames{
		models: map[string]bool{"gpt-4o": true},
		tokens: map[uint]bool{7: true},
	}
	members := `[{"ref":"gpt-4o","priority":0,"weight":1}]`
	cases := []struct {
		name     string
		routing  models.ModelRouting
		wantCode string
	}{
		{name: "unknown scope", routing: models.ModelRouting{Name: "x", Scope: "team", Members: members}, wantCode: dao.ErrCodeInvalidScope},
		{name: "global with owner", routing: models.ModelRouting{Name: "x", Scope: models.RoutingScopeGlobal, UserID: 1, Members: members}, wantCode: dao.ErrCodeInvalidScopeOwner},
		{name: "user without owner", routing: models.ModelRouting{Name: "x", Scope: models.RoutingScopeUser, Members: members}, wantCode: dao.ErrCodeInvalidScopeOwner},
		{name: "token with user owner", routing: models.ModelRouting{Name: "x", Scope: models.RoutingScopeToken, UserID: 1, TokenID: 7, Members: members}, wantCode: dao.ErrCodeInvalidScopeOwner},
		{name: "missing token", routing: models.ModelRouting{Name: "x", Scope: models.RoutingScopeToken, TokenID: 8, Members: members}, wantCode: dao.ErrCodeTokenNotFound},
		{name: "existing token", routing: models.ModelRouting{Name: "x", Scope: models.RoutingScopeToken, TokenID: 7, Members: members}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := dao.ValidateRouting(&tc.routing, nv)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil || err.Code() != tc.wantCode {
				t.Fatalf("code = %v, want %s", err, tc.wantCode)
			}
		})
	}
}

func TestValidate_MembersRules(t *testing.T) {
	nv := &fakeNames{models: map[string]bool{"gpt-4o": true}}
	err := dao.ValidateRouting(&models.ModelRouting{Name: "x", Scope: "global", Members: `[]`}, nv)
	if err == nil || err.Code() != "members_empty" {
		t.Errorf("want members_empty, got %v", err)
	}
	big := `[`
	for i := 0; i < 33; i++ {
		if i > 0 {
			big += ","
		}
		big += `{"ref":"gpt-4o","priority":0,"weight":1}`
	}
	big += `]`
	err = dao.ValidateRouting(&models.ModelRouting{Name: "x", Scope: "global", Members: big}, nv)
	if err == nil || err.Code() != "members_too_many" {
		t.Errorf("want members_too_many, got %v", err)
	}
}

func TestValidate_InvalidRef(t *testing.T) {
	nv := &fakeNames{models: map[string]bool{"gpt-4o": true}}
	err := dao.ValidateRouting(&models.ModelRouting{
		Name: "x", Scope: "global",
		Members: `[{"ref":"nonexistent","priority":0,"weight":1}]`,
	}, nv)
	if err == nil || err.Code() != "invalid_ref" {
		t.Fatalf("want invalid_ref, got %v", err)
	}
	if d := err.Details()["ref"]; d != "nonexistent" {
		t.Errorf("want details.ref=nonexistent, got %v", d)
	}
}

func TestValidate_CycleDetected(t *testing.T) {
	a := &models.ModelRouting{ID: 1, Name: "A", Scope: "global", Members: `[{"ref":"B","priority":0,"weight":1}]`}
	b := &models.ModelRouting{ID: 2, Name: "B", Scope: "global", Members: `[{"ref":"A","priority":0,"weight":1}]`}
	nv := &fakeNames{routings: map[string]*models.ModelRouting{"B": b}}
	err := dao.ValidateRouting(a, nv)
	if err == nil || err.Code() != "cycle_detected" {
		t.Fatalf("want cycle_detected, got %v", err)
	}
	nv.routings["A"] = a
	err = dao.ValidateRouting(b, nv)
	if err == nil || err.Code() != "cycle_detected" {
		t.Fatalf("want cycle_detected, got %v", err)
	}
}

func TestValidate_MaxDepthExceeded(t *testing.T) {
	nv := &fakeNames{routings: map[string]*models.ModelRouting{}, models: map[string]bool{"deepseek-v3": true}}
	chain := []string{"A", "B", "C", "D", "E", "F"}
	for i := 0; i < len(chain)-1; i++ {
		nv.routings[chain[i]] = &models.ModelRouting{
			Name: chain[i], Scope: "global",
			Members: `[{"ref":"` + chain[i+1] + `","priority":0,"weight":1}]`,
		}
	}
	nv.routings["F"] = &models.ModelRouting{
		Name: "F", Scope: "global",
		Members: `[{"ref":"deepseek-v3","priority":0,"weight":1}]`,
	}
	err := dao.ValidateRouting(nv.routings["A"], nv)
	if err == nil || err.Code() != "max_depth" {
		t.Fatalf("want max_depth, got %v", err)
	}
}

func TestValidate_UserCannotNestUser(t *testing.T) {
	nv := &fakeNames{models: map[string]bool{"gpt-4o": true}}
	err := dao.ValidateRouting(&models.ModelRouting{
		Name: "my", Scope: "user", UserID: 1,
		Members: `[{"ref":"other-user-routing","priority":0,"weight":1}]`,
	}, nv)
	if err == nil || err.Code() != "invalid_ref" {
		t.Fatalf("want invalid_ref (other user routing not in global namespace), got %v", err)
	}
}

func TestValidate_DeleteReferencedRejected(t *testing.T) {
	target := &models.ModelRouting{ID: 1, Name: "cheap-pool", Scope: "global"}
	parent := &models.ModelRouting{ID: 2, Name: "smart", Scope: "global",
		Members: `[{"ref":"cheap-pool","priority":0,"weight":1}]`}
	np := &fakeNames{routings: map[string]*models.ModelRouting{
		"cheap-pool": target, "smart": parent,
	}}
	err := dao.ValidateDelete(target, np)
	if err == nil || err.Code() != "referenced_by" {
		t.Fatalf("want referenced_by, got %v", err)
	}
	refs := err.Details()["refs"].([]string)
	if len(refs) != 1 || refs[0] != "smart" {
		t.Errorf("want refs=[smart], got %v", refs)
	}
}

// TestValidate_DisabledRoutingStillBlocksCycle 验证 disabled routing 对环检测仍然可见。
// 这是 regression test，防止未来在 NameProvider 里重新加上 enabled 过滤。
func TestValidate_DisabledRoutingStillBlocksCycle(t *testing.T) {
	a := &models.ModelRouting{ID: 1, Name: "A", Scope: "global", Enabled: false,
		Members: `[{"ref":"B","priority":0,"weight":1}]`}
	b := &models.ModelRouting{ID: 2, Name: "B", Scope: "global", Enabled: true,
		Members: `[{"ref":"A","priority":0,"weight":1}]`}
	nv := &fakeNames{routings: map[string]*models.ModelRouting{"A": a, "B": b}}
	err := dao.ValidateRouting(b, nv)
	if err == nil || err.Code() != "cycle_detected" {
		t.Fatalf("disabled A should still be visible to cycle detection, got %v", err)
	}
}

// 路由名与真实模型同名：成员引用该名字时，应解析为真实模型，不报环。
func TestValidate_SelfNameModelAllowed(t *testing.T) {
	self := &models.ModelRouting{
		ID: 1, Name: "gpt-5.5", Scope: "global",
		Members: `[{"ref":"gpt-5.5","priority":10,"weight":1},{"ref":"gpt-4o","priority":0,"weight":1}]`,
	}
	nv := &fakeNames{
		models:   map[string]bool{"gpt-5.5": true, "gpt-4o": true},
		routings: map[string]*models.ModelRouting{"gpt-5.5": self},
	}
	if err := dao.ValidateRouting(self, nv); err != nil {
		t.Fatalf("self-name with same real model should pass, got %v", err)
	}
}

// 路由名引用自身、但不存在同名真实模型：仍是真环。
func TestValidate_SelfNameNoModelCycle(t *testing.T) {
	self := &models.ModelRouting{
		ID: 1, Name: "loop", Scope: "global",
		Members: `[{"ref":"loop","priority":0,"weight":1}]`,
	}
	nv := &fakeNames{
		models:   map[string]bool{"gpt-4o": true},
		routings: map[string]*models.ModelRouting{"loop": self},
	}
	err := dao.ValidateRouting(self, nv)
	if err == nil || err.Code() != "cycle_detected" {
		t.Fatalf("self-loop without real model should be cycle, got %v", err)
	}
}

// 间接同名：A(=真实模型A) → B → A。有真实模型 A 时放行，无则报环。
func TestValidate_IndirectSameNameModel(t *testing.T) {
	a := &models.ModelRouting{ID: 1, Name: "A", Scope: "global", Members: `[{"ref":"B","priority":0,"weight":1}]`}
	b := &models.ModelRouting{ID: 2, Name: "B", Scope: "global", Members: `[{"ref":"A","priority":0,"weight":1}]`}
	routings := map[string]*models.ModelRouting{"A": a, "B": b}

	withModelA := &fakeNames{models: map[string]bool{"A": true}, routings: routings}
	if err := dao.ValidateRouting(a, withModelA); err != nil {
		t.Fatalf("A→B→A with real model A should pass, got %v", err)
	}

	noModelA := &fakeNames{models: map[string]bool{}, routings: routings}
	err := dao.ValidateRouting(a, noModelA)
	if err == nil || err.Code() != "cycle_detected" {
		t.Fatalf("A→B→A without real model A should be cycle, got %v", err)
	}
	path := err.Details()["path"].([]string)
	if len(path) != 3 || path[0] != "A" || path[1] != "B" || path[2] != "A" {
		t.Errorf("want cycle path [A B A], got %v", path)
	}
}
