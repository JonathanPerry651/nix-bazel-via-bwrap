package sandbox

import (
	"testing"
)

func TestResolveToHost(t *testing.T) {
	mounts := map[string]string{
		"/nix/store/abc": "/host/nix/store/abc",
		"/bin":           "/host/bin",
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"/nix/store/abc/lib/foo.so", "/host/nix/store/abc/lib/foo.so"},
		{"/bin/sh", "/host/bin/sh"},
		{"/usr/bin/env", "/usr/bin/env"},     // Not mounted, returns as-is
		{"/nix/store/xyz", "/nix/store/xyz"}, // Not mounted
	}

	for _, tt := range tests {
		got := ResolveToHost(tt.input, mounts)
		if got != tt.expected {
			t.Errorf("ResolveToHost(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
