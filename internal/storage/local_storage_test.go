package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorageSaveOpenDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewLocalStorage(dir)
	ctx := context.Background()

	path, err := s.Save(ctx, "clip.m4a", strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("Save error = %v", err)
	}
	reader, err := s.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	reader.Close()

	if err := s.Delete(ctx, path); err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after Delete: %v", err)
	}
	// Deleting twice is not an error: the insert-failure cleanup may race
	// with an operator cleaning the directory.
	if err := s.Delete(ctx, path); err != nil {
		t.Fatalf("second Delete error = %v", err)
	}
}

func TestLocalStorageDeleteRejectsEscapingPath(t *testing.T) {
	dir := t.TempDir()
	s := NewLocalStorage(filepath.Join(dir, "media"))
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if err := s.Delete(context.Background(), outside); err == nil {
		t.Fatal("Delete must refuse a path outside the storage root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
}
