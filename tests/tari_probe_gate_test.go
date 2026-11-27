package main

import "testing"

func TestProbeGate(t *testing.T) {
	cases := []struct {
		name              string
		online, validated bool
		scanned, proof    uint64
		ready             bool
	}{
		{"at tip", true, true, 1000, 1000, true},
		{"within lag behind", true, true, 998, 1000, true},
		{"too far behind", true, true, 997, 1000, false},
		{"ahead of recorded height", true, true, 1015, 1000, true},
		{"offline", false, true, 1000, 1000, false},
		{"not validated", true, false, 1000, 1000, false},
	}
	for _, c := range cases {
		if ready, reason := probeGate(c.online, c.validated, c.scanned, c.proof); ready != c.ready || ready == (reason != "") {
			t.Errorf("%s: ready=%v reason=%q", c.name, ready, reason)
		}
	}
}

func TestProbeExpectation(t *testing.T) {
	if _, _, enabled, err := probeExpectation("", ""); enabled || err != nil {
		t.Fatalf("unset must stay disabled: %v %v", enabled, err)
	}
	if total, height, enabled, err := probeExpectation("5000000", "123"); !enabled || err != nil || total != 5000000 || height != 123 {
		t.Fatalf("valid: %v %v %v %v", total, height, enabled, err)
	}
	for _, bad := range [][2]string{{"5", ""}, {"", "5"}, {"x", "5"}, {"5", "0"}, {"5", "y"}} {
		if _, _, _, err := probeExpectation(bad[0], bad[1]); err == nil {
			t.Errorf("%v must error", bad)
		}
	}
}
