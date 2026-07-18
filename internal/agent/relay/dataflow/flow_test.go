package dataflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeStep 是测试用的可编程 Step。
type fakeStep struct {
	key     string
	onApply func(p *Pass) error
}

func (f *fakeStep) Key() string { return f.key }
func (f *fakeStep) Apply(_ context.Context, p *Pass) error {
	if f.onApply != nil {
		return f.onApply(p)
	}
	return nil
}
func (f *fakeStep) Describe() StepInfo { return StepInfo{Key: f.key, Title: f.key} }

func TestFlow_Run_Order(t *testing.T) {
	var order []string
	f := &ChannelDataFlow{steps: []Step{
		&fakeStep{key: "a", onApply: func(p *Pass) error { order = append(order, "a"); return nil }},
		&fakeStep{key: "b", onApply: func(p *Pass) error { order = append(order, "b"); return nil }},
	}}
	if err := f.Run(context.Background(), &Pass{}); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("order = %v, want [a b]", order)
	}
}

func TestFlow_Run_StopsOnError(t *testing.T) {
	var ran []string
	f := &ChannelDataFlow{steps: []Step{
		&fakeStep{key: "a", onApply: func(p *Pass) error { ran = append(ran, "a"); return errors.New("boom") }},
		&fakeStep{key: "b", onApply: func(p *Pass) error { ran = append(ran, "b"); return nil }},
	}}
	err := f.Run(context.Background(), &Pass{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "step a") {
		t.Fatalf("error should be wrapped with step key, got: %v", err)
	}
	if len(ran) != 1 || ran[0] != "a" {
		t.Fatalf("ran = %v, want only [a]", ran)
	}
}

func TestFlow_Run_StopsOnAbort(t *testing.T) {
	var ran []string
	f := &ChannelDataFlow{steps: []Step{
		&fakeStep{key: "a", onApply: func(p *Pass) error { ran = append(ran, "a"); p.Aborted = true; return nil }},
		&fakeStep{key: "b", onApply: func(p *Pass) error { ran = append(ran, "b"); return nil }},
	}}
	if err := f.Run(context.Background(), &Pass{}); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if len(ran) != 1 || ran[0] != "a" {
		t.Fatalf("ran = %v, want only [a] (abort stops chain)", ran)
	}
}

func TestFlow_Describe(t *testing.T) {
	f := &ChannelDataFlow{steps: []Step{&fakeStep{key: "a"}, &fakeStep{key: "b"}}}
	infos := f.Describe()
	if len(infos) != 2 || infos[0].Key != "a" || infos[1].Key != "b" {
		t.Fatalf("Describe = %+v", infos)
	}
}
