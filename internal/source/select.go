package source

import (
	"encoding/json"
	"strings"

	"go.yaml.in/yaml/v3"
)

// selects refuses syntax whose meaning the object-only selector cannot serve.
func (r *reader) selects(n *yaml.Node, key string) []string {
	paths := r.strs(n, key)
	for _, path := range paths {
		for i, part := range strings.Split(path, ".") {
			if strings.TrimSpace(part) == "" || strings.ContainsAny(part, "[]*") || i > 0 && strings.Trim(part, "0123456789") == "" {
				r.errf(n, key, "want dot-separated object fields with nonempty segments; wildcards and array indexing are not supported")
				break
			}
		}
	}
	return paths
}

// selectedField always reads dots as traversal, even beside a literal dotted key.
func selectedField(obj map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		value, ok := obj[part]
		if !ok || i == len(parts)-1 {
			return value, ok
		}
		var next map[string]json.RawMessage
		if json.Unmarshal(value, &next) != nil {
			return nil, false
		}
		obj = next
	}
	return nil, false
}
