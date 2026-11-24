package database

import "testing"

func TestPrefixQuery(t *testing.T) {
	tests := []struct {
		search, want string
	}{
		{"bio", "bio:*"},
		{"Luke Smi", "luke:* & smi:*"},
		{"based.cooking", "based:* & cooking:*"},
		{"don't", "don:* & t:*"},
		{"a&b|c!(d):*", "a:* & b:* & c:* & d:*"},
		{"Ünïcode 2", "ünïcode:* & 2:*"},
		{"  ", ""},
		{"!?", ""},
	}
	for _, tt := range tests {
		if got := prefixQuery(tt.search); got != tt.want {
			t.Errorf("prefixQuery(%q) = %q, want %q", tt.search, got, tt.want)
		}
	}
}
