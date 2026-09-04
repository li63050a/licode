package agent

import "testing"

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		in       string
		contains bool
	}{
		{"key: sk-abc1234567890xyz", true},
		{"user token = abcdefghijklmnop", true},
		{"plain text hello world", false},
	}
	for _, c := range cases {
		got := RedactSecrets(c.in)
		if c.contains && got == c.in {
			t.Fatalf("RedactSecrets(%q) did not redact -> %q", c.in, got)
		}
		if !c.contains && got != c.in {
			t.Fatalf("RedactSecrets(%q) over-redacted -> %q", c.in, got)
		}
	}
}
