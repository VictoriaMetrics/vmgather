package services

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// stagingFile appends export output straight to the staging file. An attempt marks the current size
// before it starts and rolls back to it when it fails, so the staging file holds only completed attempts
// and nothing is written twice. Copying a separate attempt file into staging cost 1.3-6.4s per GB.
type stagingFile struct {
	f *os.File
	w *bufio.Writer
}

// openStagingFile opens the file for writing at its end. It does not use O_APPEND: on Windows Go opens
// such a file with append-only access, and Truncate in rollback then fails with "Access is denied".
func openStagingFile(path string) (*stagingFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("failed to open staging file: %w", err)
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to open staging file: %w", err)
	}
	return &stagingFile{f: f, w: bufio.NewWriterSize(f, writeBufferSize)}, nil
}

func (s *stagingFile) Write(p []byte) (int, error) {
	return s.w.Write(p)
}

// mark flushes buffered output and returns the file size: the point a failed attempt rolls back to.
func (s *stagingFile) mark() (int64, error) {
	if err := s.w.Flush(); err != nil {
		return 0, fmt.Errorf("failed to write staging file: %w", err)
	}
	info, err := s.f.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat staging file: %w", err)
	}
	return info.Size(), nil
}

// rollback drops everything written after mark returned size.
func (s *stagingFile) rollback(size int64) error {
	s.w.Reset(s.f)
	if err := s.f.Truncate(size); err != nil {
		return fmt.Errorf("failed to roll back staging file: %w", err)
	}
	if _, err := s.f.Seek(size, io.SeekStart); err != nil {
		return fmt.Errorf("failed to roll back staging file: %w", err)
	}
	return nil
}

// close flushes and closes the file. It is safe to call more than once.
func (s *stagingFile) close() error {
	if s.f == nil {
		return nil
	}
	err := s.w.Flush()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	s.f = nil
	return err
}
