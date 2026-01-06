package cache

import (
	"archive/tar"
	"compress/bzip2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"
)

// UnpackNar unpacks a NAR archive to the specified directory.
// The NAR format is Nix's archive format, which is different from tar.
// For simplicity, we first decompress, then parse the NAR format.
//
// NAR format structure:
// - "nix-archive-1" (magic bytes)
// - "(" type "regular"/"directory"/"symlink" ... ")"
func UnpackNar(reader io.Reader, compression string, destDir string) error {
	// First, decompress based on compression type
	var decompressed io.Reader
	var err error

	switch compression {
	case "xz":
		decompressed, err = xz.NewReader(reader)
		if err != nil {
			return fmt.Errorf("failed to create xz reader: %w", err)
		}
	case "bzip2":
		decompressed = bzip2.NewReader(reader)
	case "none", "":
		decompressed = reader
	default:
		return fmt.Errorf("unsupported compression: %s", compression)
	}

	// Parse and extract NAR
	return parseNar(decompressed, destDir)
}

// parseNar parses the NAR format and extracts files.
// NAR format is a simple S-expression-like format.
func parseNar(r io.Reader, destDir string) error {
	// Read magic using NarReader to handle length prefix
	nr := &NarReader{r: r}
	magic, err := nr.readString()
	if err != nil {
		return fmt.Errorf("failed to read magic: %w", err)
	}
	if magic != "nix-archive-1" {
		return fmt.Errorf("not a NAR archive (magic: %q)", magic)
	}

	// For now, use a simpler approach: if xz decompression succeeded,
	// the data should be a valid NAR. We'll implement a basic parser.
	return extractNarEntry(r, destDir, "")
}

// NarReader wraps a reader with NAR-specific parsing utilities.
type NarReader struct {
	r io.Reader
}

func (nr *NarReader) readString() (string, error) {
	// NAR strings are: 8-byte little-endian length + data + padding to 8
	lenBuf := make([]byte, 8)
	if _, err := io.ReadFull(nr.r, lenBuf); err != nil {
		return "", err
	}
	length := int64(lenBuf[0]) | int64(lenBuf[1])<<8 | int64(lenBuf[2])<<16 | int64(lenBuf[3])<<24
	if length == 0 {
		return "", nil
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(nr.r, data); err != nil {
		return "", err
	}
	// Skip padding
	padLen := (8 - (length % 8)) % 8
	if padLen > 0 {
		pad := make([]byte, padLen)
		io.ReadFull(nr.r, pad)
	}
	return string(data), nil
}

// extractNarEntry extracts a NAR entry (file, directory, symlink).
func extractNarEntry(r io.Reader, baseDir, name string) error {
	nr := &NarReader{r: r}

	// Read opening paren
	token, err := nr.readString()
	if err != nil {
		return err
	}
	if token != "(" {
		return fmt.Errorf("expected '(', got %q", token)
	}

	// Read "type"
	token, _ = nr.readString()
	if token != "type" {
		return fmt.Errorf("expected 'type', got %q", token)
	}

	// Read type value
	entryType, _ := nr.readString()

	destPath := filepath.Join(baseDir, name)

	switch entryType {
	case "regular":
		return extractRegularFile(nr, destPath)
	case "directory":
		return extractDirectory(nr, destPath)
	case "symlink":
		return extractSymlink(nr, destPath)
	default:
		return fmt.Errorf("unknown entry type: %s", entryType)
	}
}

func extractRegularFile(nr *NarReader, destPath string) error {
	var executable bool
	var contents []byte

	for {
		token, err := nr.readString()
		if err != nil {
			return err
		}
		if token == ")" {
			break
		}

		switch token {
		case "executable":
			// Next token is empty string
			nr.readString()
			executable = true
		case "contents":
			data, err := readContents(nr)
			if err != nil {
				return err
			}
			contents = data
		}
	}

	// Handle case where destPath is an existing directory (single-file NAR at root)
	if info, err := os.Stat(destPath); err == nil && info.IsDir() {
		// Use the basename of the store path as the filename, or "content" if unknown
		destPath = filepath.Join(destPath, "content")
	} else {
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
	}

	mode := os.FileMode(0644)
	if executable {
		mode = 0755
	}

	return os.WriteFile(destPath, contents, mode)
}

func readContents(nr *NarReader) ([]byte, error) {
	// Read length
	lenBuf := make([]byte, 8)
	if _, err := io.ReadFull(nr.r, lenBuf); err != nil {
		return nil, err
	}
	length := int64(lenBuf[0]) | int64(lenBuf[1])<<8 | int64(lenBuf[2])<<16 | int64(lenBuf[3])<<24

	data := make([]byte, length)
	if _, err := io.ReadFull(nr.r, data); err != nil {
		return nil, err
	}

	// Skip padding
	padLen := (8 - (length % 8)) % 8
	if padLen > 0 {
		pad := make([]byte, padLen)
		io.ReadFull(nr.r, pad)
	}

	return data, nil
}

func extractDirectory(nr *NarReader, destPath string) error {
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return err
	}

	for {
		token, err := nr.readString()
		if err != nil {
			return err
		}
		if token == ")" {
			break
		}
		if token != "entry" {
			return fmt.Errorf("expected 'entry' or ')', got %q", token)
		}

		// Read entry
		nr.readString() // "("
		nr.readString() // "name"
		entryName, _ := nr.readString()
		nr.readString() // "node"

		if err := extractNarEntry(nr.r, destPath, entryName); err != nil {
			return err
		}

		nr.readString() // ")"
	}

	return nil
}

func extractSymlink(nr *NarReader, destPath string) error {
	var target string

	for {
		token, err := nr.readString()
		if err != nil {
			return err
		}
		if token == ")" {
			break
		}
		if token == "target" {
			target, _ = nr.readString()
		}
	}

	// Create parent directories first
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	// Skip symlinks that would dangle:
	// 1. Absolute symlinks to /nix/store (resolved at runtime via mounts)
	// 2. Relative symlinks where target doesn't exist (e.g., points to other store paths)
	if strings.HasPrefix(target, "/nix/store/") {
		return nil
	}

	// For relative symlinks, check if target would exist
	resolvedTarget := target
	if !filepath.IsAbs(target) {
		resolvedTarget = filepath.Join(filepath.Dir(destPath), target)
	}
	if _, err := os.Stat(resolvedTarget); os.IsNotExist(err) {
		// Target doesn't exist - skip this symlink to avoid Bazel validation error
		return nil
	}

	return os.Symlink(target, destPath)
}

// UnpackTarXz is a fallback for tar.xz archives (used by some derivations).
func UnpackTarXz(reader io.Reader, destDir string) error {
	xzReader, err := xz.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create xz reader: %w", err)
	}

	tarReader := tar.NewReader(xzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar error: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		}
	}

	return nil
}
