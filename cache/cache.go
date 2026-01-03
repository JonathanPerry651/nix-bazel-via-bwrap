// Package cache provides functionality to query the Nix binary cache
// during Gazelle runs. This is used to populate the lockfile with NAR URLs
// and hashes, which are then used by the module extension to generate
// Bazel http_file rules for reproducible downloads.
//
// Architecture:
//  1. Gazelle queries cache.nixos.org for .narinfo files (this package)
//  2. NAR URLs and hashes are written to nix.lock
//  3. Module extension reads nix.lock and generates http_file rules
//  4. Build rules unpack the downloaded NAR files
package cache

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultCacheURL is the default Nix binary cache.
const DefaultCacheURL = "https://cache.nixos.org"

// Cache represents a Nix binary cache.
type Cache struct {
	URL    string
	client *http.Client
}

// New creates a new Cache client.
func New(url string) *Cache {
	if url == "" {
		url = DefaultCacheURL
	}
	return &Cache{
		URL:    strings.TrimSuffix(url, "/"),
		client: &http.Client{},
	}
}

// LookupNarInfo fetches the .narinfo for a given store path hash.
// Returns nil, nil if the path is not in the cache.
func (c *Cache) LookupNarInfo(storeHash string) (*NarInfo, error) {
	url := fmt.Sprintf("%s/%s.narinfo", c.URL, storeHash)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch narinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not in cache
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read narinfo: %w", err)
	}

	return ParseNarInfo(string(body))
}

// DownloadNar downloads a NAR file and returns a reader.
func (c *Cache) DownloadNar(narPath string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/%s", c.URL, narPath)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download NAR: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

// IsCached checks if a store path is available in the cache.
func (c *Cache) IsCached(storeHash string) (bool, error) {
	info, err := c.LookupNarInfo(storeHash)
	if err != nil {
		return false, err
	}
	return info != nil, nil
}
