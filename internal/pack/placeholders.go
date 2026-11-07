package pack

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Operator is the placeholder for the yard's operator: a role or stage file
// says {{operator}} where the import puts that person's name. The other
// placeholders are the pack's crew, {{<name>}}, each filled with the
// yard's crew member that takes it. A pack names no person and no crew member of
// a yard, so these are how its prose refers to them.
const Operator = "operator"

// placeholder is anything between double braces on one line. A name the
// import does not fill is an error of the pack, so what is installed never
// carries one.
var placeholder = regexp.MustCompile(`\{\{[^{}\n]*\}\}`)

// Fill puts names where text has their placeholders: {{<key>}} becomes the
// key's value. A placeholder without a key in names is left as it is.
func Fill(text []byte, names map[string]string) []byte {
	return placeholder.ReplaceAllFunc(text, func(m []byte) []byte {
		if v, ok := names[string(m[2:len(m)-2])]; ok {
			return []byte(v)
		}
		return m
	})
}

// Filled reports whether the import fills the placeholders of the pack's
// file rel: it does for a role and a stage file, and copies a script and the
// frame as they are, where braces are the shell's or the page's own. A script
// is what a run: group names, and that also makes its copy executable (the
// file's own mode says nothing, since a pack yardr embeds has none); the
// frame is copied as it is and runs nowhere.
func (p *Pack) Filled(rel string) bool {
	return !p.script(rel) && !p.isFrame(rel)
}

// Placeholders are the names a role or stage file of the pack may put in
// double braces: the operator, then the pack's crew.
func (p *Pack) Placeholders() []string {
	out := []string{Operator}
	for _, r := range p.Crew {
		if r.Name != Operator {
			out = append(out, r.Name)
		}
	}
	return out
}

// placeholderErrors names every placeholder in the pack's role and stage
// files that an import would not fill, by file and line.
func (p *Pack) placeholderErrors() Errors {
	files := p.Files()
	for _, f := range p.Flows {
		for _, s := range f.Stages {
			if s.File != "" && !slices.Contains(files, s.File) {
				files = append(files, s.File)
			}
		}
	}
	known := p.Placeholders()
	may := make([]string, len(known))
	for i, name := range known {
		may[i] = "{{" + name + "}}"
	}
	var errs Errors
	for _, rel := range files {
		if !p.Filled(rel) {
			continue
		}
		file := joinName(p.Dir, rel)
		text, err := p.read(rel)
		if err != nil {
			errs = append(errs, Error{File: file, Msg: err.Error()})
			continue
		}
		for i, line := range strings.Split(string(text), "\n") {
			for _, m := range placeholder.FindAllString(line, -1) {
				if name := m[2 : len(m)-2]; !slices.Contains(known, name) {
					errs = append(errs, Error{File: file, Line: i + 1, Msg: fmt.Sprintf("placeholder %s: an import fills %s (the operator and the pack's crew) and nothing else", m, strings.Join(may, ", "))})
				}
			}
		}
	}
	return errs
}
