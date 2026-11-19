package source

import (
	"encoding/json"
	"strings"

	"go.yaml.in/yaml/v3"
)

// selects reads answer.select. Any non-empty name is kept. A stored
// declaration is read again on use, and before dotted paths an entry was
// only an exact top-level key, so a name such as "tags[]", one containing
// "*" or "..", or "errors.404" still has to load. An empty entry is already
// refused by str, which is the one shape that never loaded. A name the
// object walk cannot follow stays that exact key.
func (r *reader) selects(n *yaml.Node, key string) []string {
	return r.strs(n, key)
}

// selectedField takes an exact top-level key when present and steps through
// nested objects along dotted segments only when it is absent and the path
// is one the walk can follow.
func selectedField(obj map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	if value, ok := obj[path]; ok {
		return value, true
	}
	if !objectPath(path) {
		return nil, false
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		value, ok := obj[part]
		if !ok || i == len(parts)-1 {
			return value, ok
		}
		var next map[string]json.RawMessage
		if json.Unmarshal(value, &next) != nil || next == nil {
			return nil, false
		}
		obj = next
	}
	return nil, false
}

// objectPath reports whether path is dot-separated object fields. A blank
// segment, a star or a bracket is not walked: that name is only the exact
// key a declaration stored before dotted paths selected, and the walk does
// not index arrays or expand wildcards. A numeric segment is an object key,
// as in errors.404.
func objectPath(path string) bool {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.ContainsAny(part, "[]*") {
			return false
		}
	}
	return true
}
