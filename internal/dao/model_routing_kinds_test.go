package dao_test

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"testing"
)

func TestValidate_ExplicitKindsAndAllBranches(t *testing.T) {
	for _, tc := range []struct{ name, members, code string }{
		{"same-name real model", `[{"kind":"model","ref":"A"}]`, ""},
		{"self routing despite real model", `[{"kind":"routing","ref":"A"}]`, "cycle_detected"},
		{"missing kind", `[{"ref":"A"}]`, "invalid_member_kind"},
		{"unknown kind", `[{"kind":"auto","ref":"A"}]`, "invalid_member_kind"},
		{"model must exist as model", `[{"kind":"model","ref":"B"}]`, "invalid_ref"},
		{"routing must exist as routing", `[{"kind":"routing","ref":"real"}]`, "invalid_ref"},
		{"cycle in low priority branch", `[{"kind":"model","ref":"real","priority":10},{"kind":"routing","ref":"B","priority":0}]`, "cycle_detected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &models.ModelRouting{ID: 1, Name: "A", Scope: "global", Members: tc.members}
			b := &models.ModelRouting{ID: 2, Name: "B", Scope: "global", Enabled: false, Members: `[{"kind":"routing","ref":"A"}]`}
			np := &fakeNames{models: map[string]bool{"A": true, "real": true}, routings: map[string]*models.ModelRouting{"A": a, "B": b}}
			err := dao.ValidateRouting(a, np)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Code() != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
}

func TestValidate_DiamondAndAncestorDepth(t *testing.T) {
	rows := map[string]*models.ModelRouting{}
	for name, members := range map[string]string{
		"A": `[{"kind":"routing","ref":"B"},{"kind":"routing","ref":"C"}]`,
		"B": `[{"kind":"routing","ref":"D"}]`, "C": `[{"kind":"routing","ref":"D"}]`,
		"D": `[{"kind":"model","ref":"real"}]`,
	} {
		rows[name] = &models.ModelRouting{Name: name, Scope: "global", Members: members}
	}
	np := &fakeNames{models: map[string]bool{"real": true}, routings: rows}
	if err := dao.ValidateRouting(rows["A"], np); err != nil {
		t.Fatalf("shared descendants are not cycles: %v", err)
	}
	rows["E"] = &models.ModelRouting{Name: "E", Scope: "global", Members: `[{"kind":"routing","ref":"F"}]`}
	rows["F"] = &models.ModelRouting{Name: "F", Scope: "global", Members: `[{"kind":"model","ref":"real"}]`}
	next := *rows["D"]
	next.Members = `[{"kind":"routing","ref":"E"}]`
	if err := dao.ValidateRouting(&next, np); err != nil {
		t.Fatalf("depth five is valid: %v", err)
	}
	rows["G"] = &models.ModelRouting{Name: "G", Scope: "global", Members: `[{"kind":"model","ref":"real"}]`}
	rows["F"].Members = `[{"kind":"routing","ref":"G"}]`
	if err := dao.ValidateRouting(&next, np); err == nil || err.Code() != "max_depth" {
		t.Fatalf("edit must check ancestor paths: %v", err)
	}
}

func TestValidate_DeleteIgnoresSameNameModelReferences(t *testing.T) {
	target := &models.ModelRouting{ID: 1, Name: "same", Scope: "global"}
	parent := &models.ModelRouting{ID: 2, Name: "parent", Scope: "global", Members: `[{"kind":"model","ref":"same"}]`}
	np := &fakeNames{routings: map[string]*models.ModelRouting{"same": target, "parent": parent}}
	if err := dao.ValidateDelete(target, np); err != nil {
		t.Fatal(err)
	}
	parent.Members = `[{"kind":"routing","ref":"same"}]`
	if err := dao.ValidateDelete(target, np); err == nil || err.Code() != "referenced_by" {
		t.Fatalf("routing reference must block deletion: %v", err)
	}
}
