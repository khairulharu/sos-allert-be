package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStorage saves incident media under a directory on disk.
// It is the default backend; S3/GCS needs credentials and is a
// follow-up (see README). Filenames are sanitised to a base name.
type LocalStorage struct {
	Dir string
}

// NewLocalStorage returns a storage rooted at dir.
func NewLocalStorage(dir string) *LocalStorage {
	return &LocalStorage{Dir: dir}
}

// Save writes r to dir/filename and returns the storage path.
func (s *LocalStorage) Save(ctx context.Context, filename string, r io.Reader) (string, error) {
	_ = ctx
	name := filepath.Base(filename)
	if name == "." || name == "/" || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid filename %q", filename)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create storage dir: %w", err)
	}
	path := filepath.Join(s.Dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create storage file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("write storage file: %w", err)
	}
	return path, nil
}
