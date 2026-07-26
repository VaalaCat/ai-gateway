package models

import "testing"

func TestNormalizeTokenTraceModeForWrite(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want TokenTraceMode
		err  bool
	}{
		{name: "empty defaults full", in: "", want: TokenTraceModeFull},
		{name: "full accepted", in: "full", want: TokenTraceModeFull},
		{name: "headers accepted", in: "headers", want: TokenTraceModeHeaders},
		{name: "unknown rejected", in: "body", err: true},
		{name: "whitespace rejected", in: " headers ", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeTokenTraceModeForWrite(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("err=%v wantErr=%v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestTokenTraceModeForRuntime(t *testing.T) {
	for _, tc := range []struct {
		in      TokenTraceMode
		want    TokenTraceMode
		unknown bool
	}{
		{in: "", want: TokenTraceModeFull},
		{in: TokenTraceModeFull, want: TokenTraceModeFull},
		{in: TokenTraceModeHeaders, want: TokenTraceModeHeaders},
		{in: "future", want: TokenTraceModeFull, unknown: true},
	} {
		got, unknown := tc.in.ForRuntime()
		if got != tc.want || unknown != tc.unknown {
			t.Fatalf("ForRuntime(%q)=(%q,%v), want (%q,%v)", tc.in, got, unknown, tc.want, tc.unknown)
		}
	}
}
