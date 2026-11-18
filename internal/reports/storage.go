package reports

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Storage writes/reads/deletes generated report files under {rootDir}/reports/{jobId}.{ext} — a
// sibling of internal/documents' LocalStorage, but simpler: reports are always locally generated
// (never migrated from Google Drive), and are read exactly once (see ReadAndDelete).
type Storage struct {
	dir string
}

func NewStorage(uploadsRootDir string) *Storage {
	return &Storage{dir: filepath.Join(uploadsRootDir, "reports")}
}

// Write stores content under {dir}/{jobID}.{ext}, creating the directory as needed, and returns
// the path to persist as the job's result.filePath.
func (s *Storage) Write(jobID uuid.UUID, ext string, content []byte) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("reports: creating output directory: %w", err)
	}
	path := filepath.Join(s.dir, jobID.String()+"."+ext)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("reports: writing report file: %w", err)
	}
	return path, nil
}

// ReadAndDelete returns a report file's bytes and removes it from disk — mirrors Java's
// Files.newInputStream(path, StandardOpenOption.DELETE_ON_CLOSE): a generated report can be
// downloaded exactly once, by design (minimize retained storage on a self-hosted box). Returns
// os.ErrNotExist (check with os.IsNotExist) if the file was already downloaded or reaped.
func (s *Storage) ReadAndDelete(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(path) // best-effort — the bytes are already read; a failed delete just leaves a stale file
	return content, nil
}
