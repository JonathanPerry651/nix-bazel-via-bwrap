package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Copier handles copying files and directories while resolving /nix/store symlinks
type Copier struct {
	// Mounts maps sandbox paths to their actual host paths
	Mounts map[string]string
}

// NewCopier creates a new Copier with the given mounts map
func NewCopier(mounts map[string]string) *Copier {
	return &Copier{Mounts: mounts}
}

// CopyRecursive copies src to dst, resolving any /nix/store symlinks using the mounts map
func (c *Copier) CopyRecursive(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return c.copyDir(src, dst)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return c.copySymlink(src, dst)
	}

	return c.copyFile(src, dst, info.Mode())
}

func (c *Copier) copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := c.CopyRecursive(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (c *Copier) copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}

	// If symlink points to /nix/store, try to resolve via mounts and copy content
	if strings.HasPrefix(target, "/nix/store/") {
		hostTarget := c.resolveNixStorePath(target)
		if hostTarget != "" {
			targetInfo, err := os.Stat(hostTarget)
			if err == nil {
				if targetInfo.IsDir() {
					return c.CopyRecursive(hostTarget, dst)
				}
				return c.copyFile(hostTarget, dst, targetInfo.Mode())
			}
			fmt.Printf("WARNING: Found mount for %s -> %s but stat failed: %v\n", target, hostTarget, err)
		} else {
			fmt.Printf("WARNING: Cannot resolve /nix/store symlink %s (not in mounts map)\n", target)
		}
	}

	// Keep symlink as-is for non /nix/store targets or if resolution failed
	return os.Symlink(target, dst)
}

// resolveNixStorePath looks up a /nix/store path in the mounts map and returns the host path
func (c *Copier) resolveNixStorePath(nixPath string) string {
	// Direct match
	if hostPath, ok := c.Mounts[nixPath]; ok {
		return hostPath
	}

	// Prefix match
	for sandboxPath, hostPath := range c.Mounts {
		if sandboxPath == nixPath {
			return hostPath
		}
		if strings.HasPrefix(nixPath, sandboxPath+"/") {
			suffix := strings.TrimPrefix(nixPath, sandboxPath)
			return filepath.Join(hostPath, suffix)
		}
	}

	return ""
}

func (c *Copier) copyFile(src, dst string, mode os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()

	_, err = io.Copy(df, sf)
	if err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
