package tool

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"time"
)

// ReadRecord tracks a single file read.
type ReadRecord struct {
	Path        string
	Offset      int
	Limit       int
	ContentHash string
	MTime       time.Time
	CanDedup    bool
}

// FileState tracks reads and writes for a single session.
type FileState struct {
	mu    sync.Mutex
	reads map[string]*ReadRecord
}

// NewFileState creates a new per-session file state tracker.
func NewFileState() *FileState {
	return &FileState{reads: make(map[string]*ReadRecord)}
}

func (fs *FileState) key(path string, offset, limit int) string {
	return fmt.Sprintf("%s|%d|%d", path, offset, limit)
}

// RecordRead records a file read operation.
func (fs *FileState) RecordRead(path string, offset, limit int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hash, err := fileHash(path)
	if err != nil {
		hash = ""
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.reads[fs.key(path, offset, limit)] = &ReadRecord{
		Path:        path,
		Offset:      offset,
		Limit:       limit,
		ContentHash: hash,
		MTime:       info.ModTime(),
		CanDedup:    true,
	}
	return nil
}

// RecordWrite records a file write, invalidating stale read records.
func (fs *FileState) RecordWrite(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hash, err := fileHash(path)
	if err != nil {
		hash = ""
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	for key, rec := range fs.reads {
		if rec.Path == path {
			rec.CanDedup = false
			rec.MTime = info.ModTime()
			rec.ContentHash = hash
			_ = key
		}
	}
	return nil
}

// CheckRead returns a warning if a previously read file has changed on disk.
func (fs *FileState) CheckRead(path string) string {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for _, rec := range fs.reads {
		if rec.Path == path {
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Sprintf("File %s has been deleted since last read", path)
			}
			if !info.ModTime().Equal(rec.MTime) {
				hash, _ := fileHash(path)
				if hash != rec.ContentHash {
					return fmt.Sprintf("File %s has been modified since last read", path)
				}
			}
		}
	}
	return ""
}

// IsUnchanged returns true if the file was read with the same params and is unchanged.
func (fs *FileState) IsUnchanged(path string, offset, limit int) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	rec, ok := fs.reads[fs.key(path, offset, limit)]
	if !ok || !rec.CanDedup {
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.ModTime().Equal(rec.MTime) {
		return false
	}

	hash, _ := fileHash(path)
	return hash == rec.ContentHash
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8]), nil
}
