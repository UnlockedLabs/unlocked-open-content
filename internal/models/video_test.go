package models

import (
	"errors"
	"testing"
)

func TestValidateVideoURL(t *testing.T) {
	tests := []struct {
		url   string
		valid bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"http://example.com/video.mp4", true},
		{"ftp://example.com/video.mp4", false},
		{"https://", false},
		{"https://example.com/a;rm -rf", false},
		{"https://example.com/a|b", false},
		{"https://example.com/a$b", false},
		{"https://example.com/a`b`", false},
		{"https://example.com/a&b", false},
		{"https://example.com/a%0Ab", false},
		{"https://example.com/a%0db", false},
		{"https://example.com/a\nb", false},
		{"://bad", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := ValidateVideoURL(tt.url)
			if (err == nil) != tt.valid {
				t.Errorf("ValidateVideoURL(%q) = %v, want valid=%v", tt.url, err, tt.valid)
			}
			if err != nil && !errors.Is(err, errInvalidVideoURL) {
				t.Errorf("error %v isn't errInvalidVideoURL", err)
			}
		})
	}
}
