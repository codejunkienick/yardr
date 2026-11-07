package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"yardr.dev/yardr/internal/pack"
)

// printPlanConsent ends a plan with the lines that say what applying the pack
// consents to: its digest, the programs that run as you, and the frame whose
// script runs in a reader's browser.
func printPlanConsent(w io.Writer, p *pack.Plan) {
	// The code an import brings runs as whoever runs the yard, and applying
	// the pack is the consent to that, so the plan ends by naming the files
	// that run (docs/pack-api.md). A pack with none says nothing. The digest
	// is just above that line, when there is one, and pins these files too.
	if p.Digest != "" {
		fmt.Fprintf(w, "\ndigest sha256:%s\n", p.Digest)
	}
	if len(p.Programs) > 0 {
		fmt.Fprintf(w, "\nruns %s of the pack as you: %s\n", plural(len(p.Programs), "program"), strings.Join(p.Programs, ", "))
	}
	// A frame is no program: yardr never runs it, so it is not counted among
	// them, but its script is the pack author's and an operator who applies
	// the pack lets it into every page made under it.
	if p.Frame != nil {
		fmt.Fprintf(w, "\nframe %s (%s bytes): copied into each new page made under this pack; its script runs in a reader's browser only when the reader chooses run\n", p.Frame.File, grouped(p.Frame.Bytes))
	}
}

// grouped is n in decimal with a comma before each three digits: 11842 is
// 11,842.
func grouped(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
