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
	used, _, ok, _, _ = s.ReadIn(line)
	return used, ok
}

// ReadIn reads the context use and window from one session line, or reports
// a reset that invalidates the previous reading. A declared window or model
// field that cannot supply a window gives zero, so no percentage can be read.
// A Usage of "." reads the counts from the line itself.
// A reading of 0 is no reading: a request never sends nothing, so all-zero
// usage means the turn errored, not that the session emptied.
func (s ContextSource) ReadIn(line []byte) (used, window int, ok, reset bool, err error) {
	var v map[string]any
	if json.Unmarshal(line, &v) != nil {
		return 0, 0, false, false, nil
	}
	key := s.TypeField
	if key == "" {
		key = "type"
	}
	t, _ := valueAt(v, key).(string)
	if s.ResetType != "" && t == s.ResetType {
		return 0, 0, false, true, nil
	}
	if s.Type != "" && t != s.Type {
		return 0, 0, false, false, nil
	}
	u := v
	if s.Usage != "." {
		for _, k := range strings.Split(s.Usage, ".") {
			if u, _ = u[k].(map[string]any); u == nil {
				return 0, 0, false, false, nil
			}
		}
	}
	for _, k := range s.tokens() {
		if n, is := u[k].(float64); is {
			used, ok = used+int(n), true
		}
	}
	if !ok || used == 0 {
		// No token field, or every one present reads 0: an errored turn
		// carries no use, and the caller keeps its earlier reading. This
		// sits before the window read, as a zero line carries no window.
		return 0, 0, false, false, nil
	}
	window = s.Window
	if s.WindowField != "" {
		raw := valueAt(v, s.WindowField)
		if raw == nil {
			return used, 0, true, false, nil
		}
		n, is := raw.(float64)
		if !is || n <= 0 || n != math.Trunc(n) || n >= float64(int(^uint(0)>>1)) {
			return 0, 0, false, false, fmt.Errorf("context: %s must be a positive integer", s.WindowField)
		}
		window = int(n)
	}
	if s.ModelField != "" {
		raw := valueAt(v, s.ModelField)
		model, is := raw.(string)
		if raw != nil && !is {
			return 0, 0, false, false, fmt.Errorf("context: %s must be a model id", s.ModelField)
		}
		window = s.ModelWindows[model]
	}
	return used, window, true, false, nil
}
