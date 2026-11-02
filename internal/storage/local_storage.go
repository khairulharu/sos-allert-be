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

// Open opens a stored file for reading. The path must stay inside the
// storage root so a corrupted DB row cannot read arbitrary files.
func (s *LocalStorage) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	_ = ctx
	root, err := filepath.Abs(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage dir: %w", err)
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve storage path: %w", err)
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return nil, fmt.Errorf("storage path %q escapes %q", target, root)
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("open storage file: %w", err)
	}
	return f, nil
}
