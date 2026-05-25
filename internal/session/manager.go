package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// ---------- Session ----------

// Session represents a persisted conversation.
type Session struct {
	Key              string
	Messages         []*schema.Message
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Metadata         map[string]any
	LastConsolidated int // messages before this index have been archived
}

// AddMessage appends a message and bumps UpdatedAt.
func (s *Session) AddMessage(msg *schema.Message) {
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

// Clear resets the session to initial state.
func (s *Session) Clear() {
	s.Messages = make([]*schema.Message, 0)
	s.LastConsolidated = 0
	s.UpdatedAt = time.Now()
}

// GetHistory returns unconsolidated messages aligned to a user turn.
func (s *Session) GetHistory(maxMessages int) []*schema.Message {
	start := s.LastConsolidated
	if start < 0 {
		start = 0
	}
	if start >= len(s.Messages) {
		return nil
	}
	msgs := s.Messages[start:]
	if len(msgs) == 0 {
		return nil
	}

	firstUser := 0
	for i, m := range msgs {
		if m.Role == schema.User {
			firstUser = i
			break
		}
	}
	if firstUser > 0 {
		msgs = msgs[firstUser:]
	}

	if maxMessages > 0 && len(msgs) > maxMessages {
		msgs = msgs[len(msgs)-maxMessages:]
	}
	return msgs
}

// ---------- SessionManager ----------

// SessionManager persists sessions as JSONL files with an in-memory cache.
//
// JSONL format:
//
//	line 0: {"_type":"metadata","key":"...","created_at":"...","updated_at":"...","last_consolidated":N,...}
//	line 1+: schema.Message as JSON
type SessionManager struct {
	sessionsDir string
	cache       map[string]*Session
	mu          sync.RWMutex
}

// NewSessionManager creates a SessionManager rooted at sessionsDir.
func NewSessionManager(sessionsDir string) (*SessionManager, error) {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}
	return &SessionManager{
		sessionsDir: sessionsDir,
		cache:       make(map[string]*Session),
	}, nil
}

// GetOrCreate returns a cached session or loads from disk. Creates fresh if missing.
func (m *SessionManager) GetOrCreate(key string) *Session {
	m.mu.RLock()
	if s, ok := m.cache[key]; ok {
		m.mu.RUnlock()
		return s
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.cache[key]; ok {
		return s
	}

	s, err := m.load(key)
	if err != nil || s == nil {
		s = &Session{
			Key:       key,
			Messages:  make([]*schema.Message, 0),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Metadata:  make(map[string]any),
		}
	}
	m.cache[key] = s
	return s
}

// Save persists a session to disk atomically (tmp write + rename).
func (m *SessionManager) Save(s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.getSessionPath(s.Key)
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	metadata := map[string]any{
		"_type":             "metadata",
		"key":               s.Key,
		"created_at":        s.CreatedAt.Format(time.RFC3339),
		"updated_at":        s.UpdatedAt.Format(time.RFC3339),
		"metadata":          s.Metadata,
		"last_consolidated": s.LastConsolidated,
	}

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(metadata); err != nil {
		return err
	}

	for _, msg := range s.Messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}

	m.cache[s.Key] = s
	return nil
}

// Invalidate removes a session from cache so it reloads from disk next time.
func (m *SessionManager) Invalidate(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, key)
}

// ListSessions returns session keys found on disk.
func (m *SessionManager) ListSessions() ([]string, error) {
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(e.Name(), ".jsonl"))
	}
	return keys, nil
}

// load reads a session from its JSONL file. Returns nil if not found.
func (m *SessionManager) load(key string) (*Session, error) {
	path := m.getSessionPath(key)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var s *Session
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(line, &data); err != nil {
			continue
		}

		if data["_type"] == "metadata" {
			s = &Session{Key: key, Metadata: make(map[string]any)}
			if md, ok := data["metadata"].(map[string]any); ok {
				s.Metadata = md
			}
			if lc, ok := data["last_consolidated"].(float64); ok {
				s.LastConsolidated = int(lc)
			}
			if ca, ok := data["created_at"].(string); ok {
				s.CreatedAt, _ = time.Parse(time.RFC3339, ca)
			}
			if ua, ok := data["updated_at"].(string); ok {
				s.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
			}
		} else if s != nil {
			var msg schema.Message
			if err := json.Unmarshal(line, &msg); err == nil {
				s.Messages = append(s.Messages, &msg)
			}
		}
	}
	return s, nil
}

// getSessionPath returns the JSONL file path for a session key.
func (m *SessionManager) getSessionPath(key string) string {
	safe := strings.ReplaceAll(key, ":", "_")
	return filepath.Join(m.sessionsDir, safe+".jsonl")
}
