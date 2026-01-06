package cache

import (
	"encoding/json"
	"os"
)

// LockFile represents the nix.lock file format.
type LockFile struct {
	Version       int                    `json:"version"`
	NixpkgsCommit string                 `json:"nixpkgs_commit,omitempty"`
	Flakes        map[string]FlakeInfo   `json:"flakes"`
	SourceInfo    map[string]SourceInfo  `json:"sources"`
	StorePaths    map[string]*CacheEntry `json:"store_paths"`
}

// FlakeInfo contains info about a resolved flake.
type FlakeInfo struct {
	DrvHash         string            `json:"drv_hash"`
	Deps            []string          `json:"deps,omitempty"`            // Build deps (other flakes)
	OutputStorePath string            `json:"output_store_path"`         // Key into StorePaths
	Executable      string            `json:"executable,omitempty"`      // Path to executable inside output
	Env             map[string]string `json:"env,omitempty"`             // Exported environment variables
	Closure         []string          `json:"runtime_closure,omitempty"` // List of keys into StorePaths
}

// CacheEntry contains cache.nixos.org info for http_file generation.
type CacheEntry struct {
	StorePath   string   `json:"store_path"`
	NarURL      string   `json:"nar_url"`
	NarHash     string   `json:"nar_hash"`
	FileSize    int64    `json:"file_size"`
	Compression string   `json:"compression"`
	References  []string `json:"references,omitempty"`
}

// SourceInfo contains info for an http_file source.
type SourceInfo struct {
	URLs      []string `json:"urls"`
	Sha256    string   `json:"sha256,omitempty"`
	Integrity string   `json:"integrity,omitempty"`
	Path      string   `json:"downloaded_file_path,omitempty"`
}

// LoadLockFile loads a lockfile from disk.
func LoadLockFile(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if len(data) == 0 {
		return &LockFile{
			Version:    1,
			Flakes:     make(map[string]FlakeInfo),
			SourceInfo: make(map[string]SourceInfo),
			StorePaths: make(map[string]*CacheEntry),
		}, nil
	}

	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	return &lf, nil
}

// Save writes the lockfile to disk.
func (lf *LockFile) Save(path string) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AddStorePath adds a cache entry for a specific store path.
func (lf *LockFile) AddStorePath(info *NarInfo) {
	if lf.StorePaths == nil {
		lf.StorePaths = make(map[string]*CacheEntry)
	}

	hash := "sha256:" + info.FileHash
	if hexVal, err := NixHashToHex(info.FileHash); err == nil {
		hash = "sha256:" + hexVal
	}

	lf.StorePaths[info.StorePath] = &CacheEntry{
		StorePath:   info.StorePath,
		NarURL:      DefaultCacheURL + "/" + info.URL,
		NarHash:     hash,
		FileSize:    info.FileSize,
		Compression: info.Compression,
		References:  info.References,
	}
}

// AddFlake adds a flake entry.
func (lf *LockFile) AddFlake(label, drvHash, outputStorePath, executable string, env map[string]string, deps, closure []string) {
	lf.Flakes[label] = FlakeInfo{
		DrvHash:         drvHash,
		Deps:            deps,
		OutputStorePath: outputStorePath,
		Executable:      executable,
		Env:             env,
		Closure:         closure,
	}
}
