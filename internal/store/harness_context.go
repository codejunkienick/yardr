package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// tokens is the fields of a usage object that add up to what a session holds.
func (s ContextSource) tokens() []string {
	if len(s.Tokens) > 0 {
		return s.Tokens
	}
	return DefaultContextTokens
}

// UsageIn is the context use one line holds; ReadIn also reads its window.
func (s ContextSource) UsageIn(line []byte) (used int, ok bool) {
	used, _, ok, _ = s.ReadIn(line)
	return used, ok
}

// ReadIn reads the context use and declared window from one session line.
// A missing WindowField gives a zero window, so no percentage can be read yet.
// A present invalid window is an error; without WindowField, zero uses the model.
func (s ContextSource) ReadIn(line []byte) (used, window int, ok bool, err error) {
	var v map[string]any
	if json.Unmarshal(line, &v) != nil {
		return 0, 0, false, nil
	}
	key := s.TypeField
	if key == "" {
		key = "type"
	}
	if t, _ := valueAt(v, key).(string); s.Type != "" && t != s.Type {
		return 0, 0, false, nil
	}
	u := v
	for _, k := range strings.Split(s.Usage, ".") {
		if u, _ = u[k].(map[string]any); u == nil {
			return 0, 0, false, nil
		}
	}
	for _, k := range s.tokens() {
		if n, is := u[k].(float64); is {
			used, ok = used+int(n), true
		}
	}
	if !ok {
		return 0, 0, false, nil
	}
	window = s.Window
	if s.WindowField != "" {
		raw := valueAt(v, s.WindowField)
		if raw == nil {
			return used, 0, true, nil
		}
		n, is := raw.(float64)
		if !is || n <= 0 || n != math.Trunc(n) || n >= float64(int(^uint(0)>>1)) {
			return 0, 0, false, fmt.Errorf("context: %s must be a positive integer", s.WindowField)
		}
		window = int(n)
	}
	return used, window, true, nil
}
