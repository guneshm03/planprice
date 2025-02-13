package pricing

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// LoadFile reads a snapshot from disk (e.g. one built by pricegen) and, when
// a "<path>.sha256" file sits next to it, verifies the checksum first.
func LoadFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if sumFile, err := os.ReadFile(path + ".sha256"); err == nil {
		if err := VerifyChecksum(data, sumFile); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	s, err := Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// VerifyChecksum compares data with a "sha256sum"-style line.
func VerifyChecksum(data, sumFile []byte) error {
	line, _, _ := bufio.NewReader(bytes.NewReader(sumFile)).ReadLine()
	want, _, _ := strings.Cut(strings.TrimSpace(string(line)), " ")
	if want == "" {
		return fmt.Errorf("checksum file is empty")
	}
	if got := Digest(data); got != want {
		return fmt.Errorf("price snapshot checksum mismatch: file has %s, checksum file says %s (rebuild with pricegen instead of editing by hand)", got, want)
	}
	return nil
}

// Digest returns the hex SHA-256 of data.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
