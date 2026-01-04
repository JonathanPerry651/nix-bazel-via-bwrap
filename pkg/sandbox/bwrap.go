package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SandboxConfig defines the configuration for the bwrap sandbox
type SandboxConfig struct {
	Mounts  map[string]string // Sandbox -> Host (RO)
	Binds   map[string]string // Sandbox -> Host (RW)
	Envs    map[string]string
	WorkDir string // Inside sandbox

	// UseNamespaces enables --unshare-all, --proc /proc, --dev /dev, --tmpfs /tmp
	UseNamespaces bool

	// Explicit list of host paths to mount (e.g. .cache)
	AdditionalRoBinds []string
}

// StandardSetup adds standard mounts and enables namespaces.
// If mountSystemLibs is true, it mounts host system libraries and tools.
func (c *SandboxConfig) StandardSetup(mountSystemLibs bool) error {
	c.UseNamespaces = true

	// Initialize maps if needed
	if c.Mounts == nil {
		c.Mounts = make(map[string]string)
	}

	// Mount standard system library paths if requested
	if mountSystemLibs {
		libs := []string{
			"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib",
			"/bin", "/usr/bin", "/sbin", "/usr/sbin",
		}
		for _, lib := range libs {
			if _, err := os.Stat(lib); err == nil {
				c.Mounts[lib] = lib
			}
		}
	}
	return nil
}

// BuildBwrapArgs constructs the bwrap command line arguments
func BuildBwrapArgs(cfg *SandboxConfig) ([]string, error) {
	var args []string

	if cfg.UseNamespaces {
		args = append(args,
			"--unshare-all",
			"--proc", "/proc",
			"--dev", "/dev",
			"--tmpfs", "/tmp",
			// Ensure /usr exists if we mount stuff under it?
			"--dir", "/usr",
		)
	}

	// WorkDir
	if cfg.WorkDir != "" {
		args = append(args, "--chdir", cfg.WorkDir)
	}

	// Envs
	for k, v := range cfg.Envs {
		args = append(args, "--setenv", k, v)
	}

	// RW Binds (processed before RO binds)
	var sortedBinds []string
	for s := range cfg.Binds {
		sortedBinds = append(sortedBinds, s)
	}
	sort.Strings(sortedBinds)

	for _, s := range sortedBinds {
		h := cfg.Binds[s]
		if !strings.HasPrefix(s, "/bin/") && !strings.HasPrefix(s, "/usr/") {
			args = append(args, "--dir", filepath.Dir(s))
		}
		args = append(args, "--bind", h, s)
	}

	// Mounts (RO)
	var sortedSandbox []string
	for s := range cfg.Mounts {
		sortedSandbox = append(sortedSandbox, s)
	}
	sort.Strings(sortedSandbox)

	for _, s := range sortedSandbox {
		host := cfg.Mounts[s]
		if !strings.HasPrefix(s, "/bin/") && !strings.HasPrefix(s, "/usr/") {
			args = append(args, "--dir", filepath.Dir(s))
		}
		args = append(args, "--ro-bind", host, s)
	}

	// Additional binds
	for _, p := range cfg.AdditionalRoBinds {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}

	return args, nil
}

// FindShell checks if a shell is configured in the mounts
func FindShell(mounts map[string]string, builderPath string) bool {
	// If builderPath provided and mapped, good
	if builderPath != "" {
		if _, ok := mounts[builderPath]; ok {
			return true
		}
	}
	// Check generic paths
	if _, ok := mounts["/bin/sh"]; ok {
		return true
	}
	if _, ok := mounts["/bin/bash"]; ok {
		return true
	}
	return false
}

// EnsureShell ensures that a shell is available in the sandbox.
// If mountSystemLibs is true, it attempts to find and mount /bin/sh from host if missing.
func (c *SandboxConfig) EnsureShell(mountSystemLibs bool) error {
	// Check if common shells are already mounted
	shells := []string{"/bin/sh", "/bin/bash", "/usr/bin/env", "/usr/bin/bash"}
	for _, sh := range shells {
		if _, ok := c.Mounts[sh]; ok {
			return nil
		}
		// Check if parent directory is mounted (e.g. /bin)
		if _, ok := c.Mounts[filepath.Dir(sh)]; ok {
			return nil
		}
	}

	if !mountSystemLibs {
		// Strict mode: user must provide shell via mounts.
		// We return nil and let execution fail if shell is missing.
		return nil
	}

	// If allowed to mount system libs, try to resolve /bin/sh on host
	if realSh, err := filepath.EvalSymlinks("/bin/sh"); err == nil {
		c.Mounts["/bin/sh"] = realSh
		return nil
	}
	// Try without symlink resolution
	if _, err := os.Stat("/bin/sh"); err == nil {
		c.Mounts["/bin/sh"] = "/bin/sh"
		return nil
	}

	// Try /bin/bash
	if realBash, err := filepath.EvalSymlinks("/bin/bash"); err == nil {
		c.Mounts["/bin/sh"] = realBash
		return nil
	}

	return fmt.Errorf("no shell found in mounts or on host")
}
