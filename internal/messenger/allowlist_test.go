package messenger

import "testing"

func TestParseAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		allowed []string
		denied  []string
	}{
		{
			name:    "single phone",
			env:     "100000000001",
			allowed: []string{"100000000001"},
			denied:  []string{"100000000002"},
		},
		{
			name:    "comma-separated with whitespace",
			env:     "100000000001, 100000000002 , 100000000003",
			allowed: []string{"100000000001", "100000000002", "100000000003"},
			denied:  []string{"100000000004"},
		},
		{
			name:    "empty entries tolerated",
			env:     ",,100000000001,,",
			allowed: []string{"100000000001"},
		},
		{
			name:   "empty env: nothing allowed (fail-closed)",
			env:    "",
			denied: []string{"100000000001", ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := ParseAllowlist(tc.env)
			for _, p := range tc.allowed {
				if !a.Allows(p) {
					t.Errorf("%q should be allowed", p)
				}
			}
			for _, p := range tc.denied {
				if a.Allows(p) {
					t.Errorf("%q should NOT be allowed", p)
				}
			}
		})
	}
}

func TestAllowlistFilterAllowed(t *testing.T) {
	a := ParseAllowlist("100000000001,100000000002")
	got := a.FilterAllowed([]string{
		"100000000001",
		"999999999999", // not allowed
		"100000000002",
		"888888888888", // not allowed
	})
	want := []string{"100000000001", "100000000002"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAllowlistNilSafe(t *testing.T) {
	var a *Allowlist
	if a.Allows("100000000001") {
		t.Error("nil allowlist should not allow anything")
	}
	if a.Size() != 0 {
		t.Error("nil allowlist Size should be 0")
	}
	if a.FilterAllowed([]string{"100000000001"}) != nil {
		t.Error("nil allowlist FilterAllowed should be nil")
	}
}
