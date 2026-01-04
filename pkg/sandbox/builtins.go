package sandbox

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// HandleBuiltin simulates Nix builtin builders like fetchurl
func HandleBuiltin(builder string, srcPath string, destPath string) error {
	if strings.HasPrefix(builder, "builtin:fetchurl") {
		return handleFetchUrl(srcPath, destPath)
	}
	// Default fallback: local copy
	// Used when builder is "builtin:interaction" or just local simulation
	return handleLocalCopy(srcPath, destPath)
}

func handleLocalCopy(src, dest string) error {
	fmt.Printf("DIAGNOSTIC: Copying existing artifact %s to %s\n", src, dest)
	cmd := exec.Command("cp", "-a", src, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp failed: %v\n%s", err, string(out))
	}
	return nil
}

func handleFetchUrl(src, dest string) error {
	// Check environment variables passed by Nix
	url := os.Getenv("url")
	urls := os.Getenv("urls")
	outputHash := os.Getenv("outputHash")

	if url == "" && urls == "" {
		return fmt.Errorf("builtin:fetchurl failed: no url provided for %s", src)
	}

	var candidates []string
	if url != "" {
		candidates = append(candidates, url)
	}
	if urls != "" {
		candidates = append(candidates, strings.Split(urls, " ")...)
	}

	for _, u := range candidates {
		if u == "" {
			continue
		}
		fmt.Printf("DIAGNOSTIC: Downloading %s\n", u)

		resp, err := http.Get(u)
		if err != nil {
			fmt.Printf("WARNING: Download failed for %s: %v\n", u, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			fmt.Printf("WARNING: Download failed for %s: status %d\n", u, resp.StatusCode)
			continue
		}

		outF, err := os.Create(dest)
		if err != nil {
			return fmt.Errorf("failed to create output file %s: %v", dest, err)
		}

		hasher := sha256.New()
		multi := io.MultiWriter(outF, hasher)

		if _, err := io.Copy(multi, resp.Body); err != nil {
			outF.Close()
			fmt.Printf("WARNING: Download interrupted for %s: %v\n", u, err)
			continue
		}
		outF.Close()

		// Verify Hash
		if outputHash != "" {
			sum := hasher.Sum(nil)
			// Expect SRI sha256-...
			if strings.HasPrefix(outputHash, "sha256-") {
				encoded := strings.TrimPrefix(outputHash, "sha256-")
				dec, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					return fmt.Errorf("failed to decode SRI hash %s: %v", outputHash, err)
				}
				if string(sum) != string(dec) {
					fmt.Printf("WARNING: Hash mismatch for %s. Expected %x, got %x\n", u, dec, sum)
					continue
				}
			} else {
				fmt.Printf("WARNING: Unsupported hash format %s, skipping verification\n", outputHash)
			}
		}
		return nil // Success
	}

	return fmt.Errorf("failed to download %s from any source", src)
}
