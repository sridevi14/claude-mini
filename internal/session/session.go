package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// undoEntry records the previous state of a file before a write.
type undoEntry struct {
	path    string
	prev    string
	existed bool
}

// Session holds the transcript log and the undo stack for one run.
type Session struct {
	root    string
	logFile *os.File
	undo    []undoEntry
}

// New creates a session, writing a JSONL transcript under .mini_agent/sessions.
func New(root string) (*Session, error) {
	dir := filepath.Join(root, ".mini_agent", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := time.Now().Format("20060102-150405") + ".jsonl"
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	return &Session{root: root, logFile: f}, nil
}

// Log appends a record to the transcript.
func (s *Session) Log(kind string, payload any) {
	if s.logFile == nil {
		return
	}
	rec := map[string]any{
		"ts":      time.Now().Format(time.RFC3339),
		"kind":    kind,
		"payload": payload,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	s.logFile.Write(append(b, '\n'))
}

// RecordWrite captures a file's current contents so the write can be undone.
func (s *Session) RecordWrite(path string) {
	prev, err := os.ReadFile(path)
	s.undo = append(s.undo, undoEntry{
		path:    path,
		prev:    string(prev),
		existed: err == nil,
	})
}

// CanUndo reports whether there is a write to revert.
func (s *Session) CanUndo() bool { return len(s.undo) > 0 }

// Undo reverts the most recent recorded write, returning the affected path.
func (s *Session) Undo() (string, error) {
	if len(s.undo) == 0 {
		return "", os.ErrNotExist
	}
	e := s.undo[len(s.undo)-1]
	s.undo = s.undo[:len(s.undo)-1]
	if !e.existed {
		// file was newly created; remove it
		if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return e.path, nil
	}
	if err := os.WriteFile(e.path, []byte(e.prev), 0o644); err != nil {
		return "", err
	}
	return e.path, nil
}

// Close flushes and closes the transcript file.
func (s *Session) Close() {
	if s.logFile != nil {
		s.logFile.Close()
	}
}

// Path returns this session's transcript file path (empty if none).
func (s *Session) Path() string {
	if s.logFile == nil {
		return ""
	}
	return s.logFile.Name()
}

// Record is one entry parsed from a transcript file.
type Record struct {
	Kind    string
	Payload json.RawMessage
}

// LastTranscript returns the path of the most recent transcript that is not the
// current session's, or "" if there is none. Transcript names are timestamps, so
// lexical order matches chronological order.
func LastTranscript(root, current string) string {
	dir := filepath.Join(root, ".mini_agent", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	current = filepath.Clean(current)
	var best string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if filepath.Clean(p) == current {
			continue
		}
		if p > best {
			best = p
		}
	}
	return best
}

// ReadTranscript parses a transcript file into ordered records.
func ReadTranscript(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // tolerate long lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		recs = append(recs, Record{Kind: raw.Kind, Payload: raw.Payload})
	}
	return recs, sc.Err()
}
