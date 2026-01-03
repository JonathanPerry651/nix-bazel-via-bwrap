package cache

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// NarInfo represents parsed information from a .narinfo file.
type NarInfo struct {
	StorePath   string   // Full store path (e.g., /nix/store/abc-hello-2.12)
	URL         string   // NAR file URL (relative to cache root)
	Compression string   // Compression type (xz, zstd, etc.)
	FileHash    string   // Hash of the compressed NAR file
	FileSize    int64    // Size of the compressed NAR file
	NarHash     string   // Hash of the uncompressed NAR
	NarSize     int64    // Size of the uncompressed NAR
	References  []string // Store paths this derivation references
	Deriver     string   // Path to the .drv that built this
	Sig         []string // Signatures
}

// ParseNarInfo parses a .narinfo file content.
func ParseNarInfo(content string) (*NarInfo, error) {
	info := &NarInfo{}
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "StorePath":
			info.StorePath = value
		case "URL":
			info.URL = value
		case "Compression":
			info.Compression = value
		case "FileHash":
			info.FileHash = value
		case "FileSize":
			fmt.Sscanf(value, "%d", &info.FileSize)
		case "NarHash":
			info.NarHash = value
		case "NarSize":
			fmt.Sscanf(value, "%d", &info.NarSize)
		case "References":
			if value != "" {
				info.References = strings.Fields(value)
			}
		case "Deriver":
			info.Deriver = value
		case "Sig":
			info.Sig = append(info.Sig, value)
		}
	}

	if info.StorePath == "" || info.URL == "" {
		return nil, fmt.Errorf("invalid narinfo: missing required fields")
	}

	return info, nil
}

// StoreHash extracts the hash portion from a store path.
// E.g., "/nix/store/abc123-hello-2.12" -> "abc123"
func StoreHash(storePath string) string {
	// Remove /nix/store/ prefix
	path := strings.TrimPrefix(storePath, "/nix/store/")
	// Take everything before the first dash
	if idx := strings.Index(path, "-"); idx > 0 {
		return path[:idx]
	}
	return path
}

// StoreName extracts the name portion from a store path.
// E.g., "/nix/store/abc123-hello-2.12" -> "hello-2.12"
func StoreName(storePath string) string {
	path := strings.TrimPrefix(storePath, "/nix/store/")
	if idx := strings.Index(path, "-"); idx > 0 && idx < len(path)-1 {
		return path[idx+1:]
	}
	return path
}

// NixHashToHex converts a Nix-style Base32 hash to a standard Hex string.
func NixHashToHex(h string) (string, error) {
	if strings.HasPrefix(h, "sha256:") {
		h = h[7:]
	}
	const alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

	n := new(big.Int)
	base := big.NewInt(32)

	for i := 0; i < len(h); i++ {
		char := h[i]
		val := strings.IndexByte(alphabet, char)
		if val == -1 {
			return "", fmt.Errorf("invalid character %c", char)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(val)))
	}

	bytes := n.Bytes()
	// Pad to 32 bytes (SHA256)
	if len(bytes) < 32 {
		padding := make([]byte, 32-len(bytes))
		bytes = append(padding, bytes...)
	} else if len(bytes) > 32 {
		// Should not happen for sha256 unless hash is wrong type
		// but big.Int might have leading zero byte if interpreted signed? Unlikely for SetBytes relative.
	}

	// Nix uses little-endian for the number, so we reverse the bytes for standard Hex (Big Endian display)
	for i, j := 0, len(bytes)-1; i < j; i, j = i+1, j-1 {
		bytes[i], bytes[j] = bytes[j], bytes[i]
	}

	return hex.EncodeToString(bytes), nil
}
