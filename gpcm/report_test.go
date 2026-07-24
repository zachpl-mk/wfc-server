package gpcm

import "testing"

func TestParseMKWMMRRecord(t *testing.T) {
	tests := []struct {
		name  string
		value string
		mode  string
		mmr   int32
		valid bool
	}{
		{name: "legacy", value: "mmr=1000", mmr: 1000, valid: true},
		{name: "retro", value: "mode=retro|mmr=1234", mode: "retro", mmr: 1234, valid: true},
		{name: "ct", value: "mode=ct|mmr=2345", mode: "ct", mmr: 2345, valid: true},
		{name: "regular", value: "mode=regular|mmr=3456", mode: "regular", mmr: 3456, valid: true},
		{name: "invalid mode", value: "mode=battle|mmr=1000", valid: false},
		{name: "missing mmr", value: "mode=retro", valid: false},
		{name: "out of range", value: "mode=retro|mmr=30001", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, mmr, valid := parseMKWMMRRecord(test.value)
			if mode != test.mode || mmr != test.mmr || valid != test.valid {
				t.Fatalf("parseMKWMMRRecord(%q) = (%q, %d, %t), want (%q, %d, %t)",
					test.value, mode, mmr, valid, test.mode, test.mmr, test.valid)
			}
		})
	}
}
