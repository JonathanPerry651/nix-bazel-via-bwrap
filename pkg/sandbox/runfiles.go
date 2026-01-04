package sandbox

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// CollectNixPathsFromRunfiles discovers .nix-mounts.json files in the runfiles tree
// and returns all Nix store paths that need mounting.
func CollectNixPathsFromRunfiles(argv0 string) ([]string, error) {
	manifest := os.Getenv("RUNFILES_MANIFEST_FILE")
	var paths []string

	if manifest != "" {
		f, err := os.Open(manifest)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, " ", 2)
			if len(parts) != 2 {
				continue
			}
			// parts[0] is runfiles relative path
			if strings.HasSuffix(parts[0], ".nix-mounts.json") {
				// Read the manifest file from the host path (parts[1])
				mp, err := ParseMountManifest(parts[1])
				if err == nil {
					paths = append(paths, mp...)
				}
			}
		}
		return paths, scanner.Err()
	}

	// Fallback: Scan runfiles dir
	runfilesDir := os.Getenv("RUNFILES_DIR")
	if runfilesDir == "" {
		runfilesDir = argv0 + ".runfiles"
	}

	if _, err := os.Stat(runfilesDir); err != nil {
		return nil, nil // No runfiles found
	}

	err := filepath.Walk(runfilesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".nix-mounts.json") {
			// Resolve symlinks just in case
			realPath, err := filepath.EvalSymlinks(path)
			if err == nil {
				mp, err := ParseMountManifest(realPath)
				if err == nil {
					paths = append(paths, mp...)
				}
			}
		}
		return nil
	})

	return paths, err
}
