package sandbox

import (
	"path/filepath"
	"sort"
	"strings"
)

// ResolveToHost maps a path potentially inside the sandbox back to a host path
// using the provided mounts map.
func ResolveToHost(p string, mounts map[string]string) string {
	// Sort sandbox paths by length descending to catch most specific mount
	var sandboxPaths []string
	for s := range mounts {
		sandboxPaths = append(sandboxPaths, s)
	}
	sort.Slice(sandboxPaths, func(i, j int) bool {
		return len(sandboxPaths[i]) > len(sandboxPaths[j])
	})

	for _, s := range sandboxPaths {
		if strings.HasPrefix(p, s) {
			rel, err := filepath.Rel(s, p)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.Join(mounts[s], rel)
			}
		}
	}
	return p
}
