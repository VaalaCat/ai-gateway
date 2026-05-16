package models

import "testing"

func TestTokenFieldsEqual(t *testing.T) {
	cases := []struct {
		name      string
		tplModels string
		tplChans  []uint
		tokModels string
		tokChans  []uint
		wantEqual bool
	}{
		{
			name:      "both empty -> equal",
			tplModels: "", tplChans: nil,
			tokModels: "", tokChans: nil,
			wantEqual: true,
		},
		{
			name:      "models same content different order -> equal",
			tplModels: `["gpt-4","gpt-5"]`, tplChans: []uint{1, 2},
			tokModels: `["gpt-5","gpt-4"]`, tokChans: []uint{2, 1},
			wantEqual: true,
		},
		{
			name:      "models added -> not equal",
			tplModels: `["gpt-4","gpt-5"]`, tplChans: []uint{1, 2},
			tokModels: `["gpt-4"]`, tokChans: []uint{1, 2},
			wantEqual: false,
		},
		{
			name:      "models removed -> not equal",
			tplModels: `["gpt-4"]`, tplChans: []uint{1, 2},
			tokModels: `["gpt-4","gpt-5"]`, tokChans: []uint{1, 2},
			wantEqual: false,
		},
		{
			name:      "channels added -> not equal",
			tplModels: `["gpt-4"]`, tplChans: []uint{1, 2, 3},
			tokModels: `["gpt-4"]`, tokChans: []uint{1, 2},
			wantEqual: false,
		},
		{
			name:      "channels removed -> not equal",
			tplModels: `["gpt-4"]`, tplChans: []uint{1},
			tokModels: `["gpt-4"]`, tokChans: []uint{1, 2},
			wantEqual: false,
		},
		{
			name:      "invalid json models -> compare as strings, not equal",
			tplModels: `not-json`, tplChans: nil,
			tokModels: `["gpt-4"]`, tokChans: nil,
			wantEqual: false,
		},
		{
			name:      "empty models string vs empty array -> equal",
			tplModels: ``, tplChans: nil,
			tokModels: `[]`, tokChans: nil,
			wantEqual: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := &Token{Models: c.tokModels}
			tok.AllowedChannelIDs = c.tokChans
			got := TokenFieldsEqual(c.tplModels, c.tplChans, tok)
			if got != c.wantEqual {
				t.Fatalf("TokenFieldsEqual(%q, %v, {%q,%v}) = %v, want %v",
					c.tplModels, c.tplChans, c.tokModels, c.tokChans, got, c.wantEqual)
			}
		})
	}
}
