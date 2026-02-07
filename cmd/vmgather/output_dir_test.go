package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultOutputDirWith_EmptyHomeFallsBack(t *testing.T) {
	got := defaultOutputDirWith(
		func() (string, error) { return "", nil },
		func(string) (os.FileInfo, error) {
			t.Fatal("stat must not be called when home dir is empty")
			return nil, nil
		},
	)
	if got != "./exports" {
		t.Fatalf("expected ./exports fallback; got %q", got)
	}
}

func TestDefaultOutputDirWith_DownloadsPresentPrefersDownloads(t *testing.T) {
	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "Downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("failed to create Downloads dir: %v", err)
	}

	got := defaultOutputDirWith(
		func() (string, error) { return tmp, nil },
		os.Stat,
	)
	want := filepath.Join(downloads, "vmgather")
	if got != want {
		t.Fatalf("expected %q; got %q", want, got)
	}
}

func TestDefaultOutputDirWith_NoDownloadsFallsBack(t *testing.T) {
	tmp := t.TempDir()

	got := defaultOutputDirWith(
		func() (string, error) { return tmp, nil },
		os.Stat,
	)
	if got != "./exports" {
		t.Fatalf("expected ./exports fallback; got %q", got)
	}
}
