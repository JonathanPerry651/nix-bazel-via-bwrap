package sandbox

import (
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

	// Setup standard paths (/bin, /usr, /proc, /dev, /tmp)
	StandardSetup bool

	// Explicit list of host paths to mount if StandardSetup is true
	// (e.g. /home/.cache/bazel)
	AdditionalRoBinds []string
}

// BuildBwrapArgs constructs the bwrap command line arguments
func BuildBwrapArgs(cfg SandboxConfig) ([]string, error) {
	var args []string

	if cfg.StandardSetup {
		args = append(args,
			"--unshare-all",
			"--proc", "/proc",
			"--dev", "/dev",
			"--tmpfs", "/tmp",
			// We rely on createSysDirs (below) to create /bin and /usr/bin correctly
			// either as directories or symlinks.
			"--dir", "/usr",
		)

		// Mount system libraries and tools for host tool compatibility
		// Use symlinks if possible to preserve host structure
		sysDirs := []string{
			"/lib", "/lib64", "/usr/lib", "/usr/lib64",
			"/bin", "/usr/bin", "/sbin", "/usr/sbin",
		}
		for _, d := range sysDirs {
			info, err := os.Lstat(d)
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				// Replicate symlink
				if target, err := os.Readlink(d); err == nil {
					// bwrap --symlink <target> <dest>
					args = append(args, "--symlink", target, d)
				}
			} else {
				// Directory (or file) -> Bind
				// For safety, only bind if it exists
				args = append(args, "--ro-bind", d, d)
			}
		}
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

// EnsureShell ensures /bin/sh is mounted in the sandbox, using host shell if necessary
func EnsureShell(mounts map[string]string, builder string) {
	if FindShell(mounts, builder) {
		return
	}

	// Fallback: Mount host /bin/sh
	if realSh, err := filepath.EvalSymlinks("/bin/sh"); err == nil {
		mounts["/bin/sh"] = realSh
	} else if _, err := os.Stat("/bin/sh"); err == nil {
		mounts["/bin/sh"] = "/bin/sh"
	} else {
		// Last resort: /bin/bash?
		if realBash, err := filepath.EvalSymlinks("/bin/bash"); err == nil {
			mounts["/bin/sh"] = realBash
		}
	}
}
