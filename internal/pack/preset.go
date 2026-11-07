// Preset grants (design yardr-7d4e, B7): `yardr source grant <source>
// --preset <name>` grants one preset's calls of one source, in a terminal
// and as a run ask. The preset is resolved against the packs yardr embeds:
// the one pack of kind source declaring the source, and its preset of that
// name with the calls it grants of the source. The presets key of a pack's
// pack.yaml is read here too, beside what resolves it.
package pack

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"yardr.dev/yardr/internal/store"
)

// PresetOfSource resolves the preset of the source against the packs in
// fsys: the one pack of kind source declaring the source, and its preset
// of that name. It answers the pack's name and the preset's calls of the
// source, in file order. A source no pack declares, two packs declaring
// one source, an unknown preset, and a preset granting nothing of the
// source are each refused with the terminal grant by name, which is what
// is left where no preset names the calls.
func PresetOfSource(fsys fs.FS, source, preset string) (string, []string, error) {
	byName := func(what string) string {
		return fmt.Sprintf("yardr source grant %s %s --group <group> | --crew <crew member>", source, what)
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", nil, err
	}
	var declaring []string
	presets := map[string][]Preset{}
	kinds := map[string]string{}
	for _, e := range entries {
		if _, err := fs.Stat(fsys, e.Name()+"/"+File); err != nil {
			continue
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			continue
		}
		p, err := Load(sub, e.Name())
		if err != nil {
			return "", nil, err
		}
		kinds[p.Name] = p.Kind
		if slices.ContainsFunc(p.Declared, func(d Declared) bool { return d.Name == source }) {
			declaring = append(declaring, p.Name)
			presets[p.Name] = p.Presets
		}
	}
	slices.Sort(declaring)
	switch {
	case len(declaring) == 0:
		return "", nil, fmt.Errorf("source %s: no embedded pack declares it, so no preset names its calls: grant by name in a terminal (%s)", source, byName("read|<call>"))
	case len(declaring) > 1:
		return "", nil, fmt.Errorf("the source %s is declared by packs %s: a preset grant names one pack's preset, so grant by name in a terminal (%s)", source, strings.Join(declaring, ", "), byName("read|<call>"))
	}
	name := declaring[0]
	if kinds[name] != KindSource {
		return "", nil, fmt.Errorf("pack %s declares the source %s and is of kind %s: only a pack of kind source carries presets, so grant by name in a terminal (%s)", name, source, orKind(kinds[name]), byName("read|<call>"))
	}
	i := slices.IndexFunc(presets[name], func(pr Preset) bool { return pr.Name == preset })
	if i < 0 {
		return "", nil, fmt.Errorf("pack %s has no preset %s (yardr pack show %s prints its presets numbered): grant by name in a terminal (%s)", name, preset, name, byName("read|<call>"))
	}
	j := slices.IndexFunc(presets[name][i].Grants, func(g CallsAsked) bool { return g.Source == source })
	if j < 0 {
		return "", nil, fmt.Errorf("the preset %s of pack %s grants nothing of the source %s: grant by name in a terminal (%s)", preset, name, source, byName("read|<call>"))
	}
	return name, slices.Clone(presets[name][i].Grants[j].Calls), nil
}

// orKind says a pack's kind where the sentence needs one for a pack that
// says none.
func orKind(kind string) string {
	if kind == "" {
		return "of no kind"
	}
	return kind
}

// preset reads one named grant suggestion: an optional one-line about and
// the grants, a source the pack declares to store.GrantRead and the names
// of calls. The declarations are already read, so each word is held to its
// file here, where calls asked by a group or a crew member are held after.
func (r *reader) preset(e entry, declared []Declared) (Preset, bool) {
	pr := Preset{Name: e.key}
	ok := true
	var grants bool
	for _, k := range r.entries(e.v, e.path, "about", "grants") {
		switch k.key {
		case "about":
			s, good := r.str(k.v, k.path)
			if !good {
				ok = false
				continue
			}
			if strings.Contains(s, "\n") {
				r.errf(k.v, k.path, "about is one line, printed by pack show beside the preset's name")
				ok = false
				continue
			}
			pr.About = s
		case "grants":
			grants = true
			pr.Grants = r.presetGrants(k, declared, &ok)
		}
	}
	if e.v.Kind == yaml.MappingNode && !grants {
		r.errf(e.k, join(e.path, "grants"), "missing: the calls the preset grants of the pack's sources (grants: { <source>: [read] })")
		ok = false
	}
	return pr, ok
}

// presetGrants reads what one preset grants: a mapping of a declared
// source to store.GrantRead and the names of calls, in the words yardr
// source grant takes.
func (r *reader) presetGrants(k entry, declared []Declared, ok *bool) []CallsAsked {
	if k.v.Kind == yaml.MappingNode && len(k.v.Content) == 0 {
		r.errf(k.v, k.path, "empty: name a source the pack declares and its calls ({ <source>: [read] }), or leave the preset out")
		*ok = false
	}
	var out []CallsAsked
	for _, s := range r.entries(k.v, k.path) {
		i := slices.IndexFunc(declared, func(d Declared) bool { return d.Name == s.key })
		if i < 0 {
			declares := "nothing"
			if len(declared) > 0 {
				names := make([]string, len(declared))
				for j, d := range declared {
					names[j] = d.Name
				}
				declares = strings.Join(names, ", ")
			}
			r.errf(s.k, s.path, "the pack declares no source %s (it declares: %s): a preset grants a source the pack's sources declare", s.key, declares)
			*ok = false
			continue
		}
		words := r.strs(s.v, s.path)
		if s.v.Kind == yaml.SequenceNode && len(s.v.Content) == 0 {
			r.errf(s.v, s.path, "empty: name %s or a call ([%s]), or leave the source out", store.GrantRead, store.GrantRead)
		}
		if len(words) == 0 {
			*ok = false
			continue
		}
		d := declared[i].Declaration
		for _, w := range words {
			if w == store.GrantRead {
				continue
			}
			if _, found := d.Call(w); !found {
				names := make([]string, len(d.Calls))
				for j, c := range d.Calls {
					names[j] = c.Name
				}
				r.errf(s.v, s.path, "%s declares no call %s (it has: %s): grant %s, every read call, or a call by its name", declared[i].File, w, strings.Join(names, ", "), store.GrantRead)
				*ok = false
			}
		}
		out = append(out, CallsAsked{Source: s.key, Calls: words})
	}
	return out
}
