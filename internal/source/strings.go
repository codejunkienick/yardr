package source

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

func (r *reader) strs(n *yaml.Node, key string) []string {
	return r.strsWithDuplicate(n, key, func(s string) string { return s + " is listed twice" })
}

// strsWithDuplicate lets a field use its own message for a repeated value
// while keeping the list parsing and duplicate removal in one place.
func (r *reader) strsWithDuplicate(n *yaml.Node, key string, message func(string) string) []string {
	if n.Kind != yaml.SequenceNode {
		r.errf(n, key, "want a list ([a, b]), got %s", describe(n))
		return nil
	}
	var out []string
	for _, c := range n.Content {
		s, ok := r.str(c, key)
		if !ok {
			continue
		}
		if slices.Contains(out, s) {
			r.errf(c, key, "%s", message(s))
			continue
		}
		out = append(out, s)
	}
	return out
}
