package sandbox

import (
	"encoding/json"
	"os"
	"strings"
)

// ParseMountManifest reads a JSON file mapping bazel paths to store paths
// and returns the list of /nix/store paths to mount.
func ParseMountManifest(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Schema: map[string]string (bazel path -> store path)
	var mounts map[string]string
	if err := json.Unmarshal(data, &mounts); err != nil {
		return nil, err
	}

	var paths []string
	for _, p := range mounts {
		if strings.HasPrefix(p, "/nix/store") {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
