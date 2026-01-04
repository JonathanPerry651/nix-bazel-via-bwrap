package sandbox

import (
	"encoding/json"
	"os"
	"strings"
)

// ParseMountManifest reads a JSON file mapping bazel paths to store paths
// and returns the list of /nix/store paths to mount.
func ParseMountManifest(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Schema: map[string]string (bazel path -> store path)
	var mounts map[string]string
	if err := json.Unmarshal(data, &mounts); err != nil {
		return nil, err
	}

	var paths = make(map[string]string)
	for k, v := range mounts {
		if strings.HasPrefix(v, "/nix/store") {
			// k is runfiles path, v is store path
			paths[k] = v
		}
	}
	return paths, nil
}

// ParseEnvManifest reads a JSON file mapping env keys to values
func ParseEnvManifest(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var envs map[string]string
	if err := json.Unmarshal(data, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}
