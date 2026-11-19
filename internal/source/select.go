package source

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// selects reads answer.select. Any non-empty name is kept. A stored
// declaration is read again on use, and before dotted paths an entry was
// only an exact top-level key, so a name such as "tags[]", one containing
// "*" or "..", or "errors.404" still has to load. An empty entry is already
// refused by str, which is the one shape that never loaded. A name the
// object walk cannot follow stays that exact key, and SelectWarnings says
// so wherever a declaration is shown, since a typo for a path would
// otherwise select nothing and say nothing.
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
	return strings.Contains(path, ".") && plainFields(path)
}

// plainFields reports whether every dot-separated part of name is a field
// the walk can follow, which means it is not blank and holds no star or
// bracket. A name with no dot is one part, so it is plain unless it holds a
// star or a bracket, which a person may mean as a wildcard or an index.
func plainFields(name string) bool {
	for _, part := range strings.Split(name, ".") {
		if strings.TrimSpace(part) == "" || strings.ContainsAny(part, "[]*") {
			return false
		}
	}
	return true
}

// SelectWarnings says, of each name in the call's answer.select that no path
// walks, what becomes of it. A name with a blank part, a star or a bracket is
// read as that exact key. It still loads, as every stored declaration must,
// so a typo for a path would select nothing in silence if nothing said so.
// Callers say it where a declaration is shown. The name is quoted, since the
// file it comes from may not be the operator's.
func (c Call) SelectWarnings() []string {
	var out []string
	for _, name := range c.Answer.Select {
		if !plainFields(name) {
			out = append(out, fmt.Sprintf("answer.select %q of call %s is read as that exact key and not walked as a path, since a path has no empty segment, star or bracket. It keeps nothing unless the answer has a key spelled exactly that way", name, c.Name))
		}
	}
	return out
}
