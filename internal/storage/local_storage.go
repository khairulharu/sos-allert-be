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
	target, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("open storage file: %w", err)
	}
	return f, nil
}

// Delete removes a stored file. It is used to drop an upload whose
// metadata insert failed, so no orphan bytes are left on disk. A file
// that is already gone is not an error.
func (s *LocalStorage) Delete(ctx context.Context, path string) error {
	_ = ctx
	target, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete storage file: %w", err)
	}
	return nil
}

// resolve maps a stored path to an absolute path inside the storage root.
func (s *LocalStorage) resolve(path string) (string, error) {
	root, err := filepath.Abs(s.Dir)
	if err != nil {
		return "", fmt.Errorf("resolve storage dir: %w", err)
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve storage path: %w", err)
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage path %q escapes %q", target, root)
	}
	return target, nil
}
