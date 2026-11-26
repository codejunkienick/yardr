package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yardr.dev/yardr/internal/store"
)

// indexSession finds a live session's conversation, allowing second-precision timestamps.
// A crew directory can be shared, so its indexed file must also start with the launch's prompt.
func (h *Herdr) indexSession(kind store.Harness, dir string, startedAt time.Time, prompt string) (string, error) {
	src := kind.Session
	home := h.harnessHome(kind.Kind, src.Home, src.HomeEnv)
	id, err := indexedSession(*src.Index, home, dir)
	if err != nil || id == "" {
		return "", err
	}
	path, err := sessionFile(src.Pattern(home, id))
	if err != nil || path == "" {
		return "", err
	}
	var start time.Time
	owns := prompt == ""
	if err := eachLine(path, func(line []byte) bool {
		start = src.Index.LineAt(line)
		if prompt != "" {
			owns = agyFirstPrompt(line, prompt)
			return true
		}
		return !start.IsZero()
	}); err != nil {
		return "", err
	}
	if !owns || start.IsZero() || start.Before(startedAt.Truncate(time.Second)) {
		return "", nil
	}
	return id, nil
}

// Antigravity wraps its first user message in USER_REQUEST and may append metadata after the wrapper.
func agyFirstPrompt(line []byte, prompt string) bool {
	var input struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if json.Unmarshal(line, &input) != nil || input.Type != "USER_INPUT" {
		return false
	}
	content, wrapped := strings.CutPrefix(input.Content, "<USER_REQUEST>\n")
	if !wrapped {
		return false
	}
	request, _, closed := strings.Cut(content, "\n</USER_REQUEST>")
	return closed && request == prompt
}

// indexedSession reads the exact directory key; absent records never fall back
// to another directory or the newest transcript.
func indexedSession(index store.SessionIndex, home, dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	path := filepath.Join(home, index.File)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read session index: %w", err)
	}
	defer f.Close()
	invalid := func() (string, error) {
		return "", fmt.Errorf("read session index %s: invalid JSON object", path)
	}
	dec := json.NewDecoder(f)
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	var matched json.RawMessage
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return invalid()
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return invalid()
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return invalid()
		}
		if name == dir {
			matched = value
		}
	}
	if last, err := dec.Token(); err != nil || last != json.Delim('}') {
		return invalid()
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid()
	}
	if matched == nil {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(matched, &value); err != nil {
		return invalid()
	}
	id, err := index.IDIn(value)
	if err != nil {
		return "", fmt.Errorf("read session index %s: %w", path, err)
	}
	return id, nil
}
