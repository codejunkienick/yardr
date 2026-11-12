package main

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// docs/architecture.md names which internal package may import which;
// this test holds the code to that page. The page is the norm and this
// test its check: the two change together, so a new import that breaks
// the rules fails the gate here. It lives in the root package because
// the gate runs root whenever a package the binary imports changes and,
// by rows of the reads table in .yardr/test, whenever a test helper or a
// generator under internal/ does (scripttest and rpc/clientgen are leaves
// the rules judge, and the binary imports neither): together every
// internal package these rules judge.
//
// The rules, quoted from the page:
// (a) internal/store imports no internal package, except internal/peer,
// which stays: it is OS process code with no yard types, so the edge
// carries none of the coupling the rule is there to stop; and
// internal/source, which stays: the store executes run asks through its
// functions, not only its types, so cutting the edge would make the two
// import each other, while source itself imports only internal/config;
// (b) a leaf imports no internal package;
// (c) no internal package imports internal/cli, and only the root main may.
const (
	internalPrefix = "yardr.dev/yardr/internal/"
	storePkg       = internalPrefix + "store"
	peerPkg        = internalPrefix + "peer"
	sourcePkg      = internalPrefix + "source"
	cliPkg         = internalPrefix + "cli"
	dispatchPkg    = internalPrefix + "dispatch"
	runnerPkg      = internalPrefix + "runner"
	serverPkg      = internalPrefix + "server"
	rootPkg        = "yardr.dev/yardr"
)

// leafPkgs is every internal package that imports no internal package:
// the page's leaves layer, measured at f75abf88 minus shared and udiff,
// which yardr-g1sf folded into store, and minus agents, which yardr-8nca
// made import store for the harness types, and plus pairing, which
// yardr-rxk9 left with the pairing paths alone, and plus pprof, which
// yardr-0bgq moved out of server so the daemon serves profiles too, and
// plus previewtest, which yardr-6dzk named in the leaves. A leaf that
// gains an internal import fails rule (b) below; a rename updates this
// list with it.
var leafPkgs = []string{
	peerPkg,
	internalPrefix + "pairing",
	internalPrefix + "pprof",
	internalPrefix + "previewtest",
	internalPrefix + "rpc/clientgen",
	internalPrefix + "scripttest",
	internalPrefix + "version",
}

// listImports runs go list over the repository and returns every
// package's direct imports, keyed by import path.
func listImports(t *testing.T) map[string][]string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go in PATH")
	}
	// The test runs in the repository root, the root package's directory.
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./...")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list ./...: %v: %s", err, stderr.String())
	}
	imports := map[string][]string{}
	for line := range strings.Lines(stdout.String()) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		imports[f[0]] = f[1:]
	}
	if len(imports) == 0 {
		t.Fatal("go list ./... printed no packages")
	}
	return imports
}

// TestImportRules runs go list over the repository and checks every
// import edge against the page's three rules. A breaking edge fails
// naming that edge.
func TestImportRules(t *testing.T) {
	t.Parallel()
	imports := listImports(t)
	for _, pkg := range append(slices.Clone(leafPkgs), storePkg, cliPkg) {
		if _, ok := imports[pkg]; !ok {
			t.Errorf("package %s is gone (renamed?): update the leaf list and the page with it", pkg)
		}
	}
	isInternal := func(p string) bool { return strings.HasPrefix(p, internalPrefix) }
	for _, from := range slices.Sorted(maps.Keys(imports)) {
		for _, to := range imports[from] {
			var rule string
			switch {
			case from == storePkg && isInternal(to) && to != peerPkg && to != sourcePkg:
				rule = "(a): internal/store imports no internal package except internal/peer and internal/source"
			case to == cliPkg && from != rootPkg:
				rule = "(c): only the root main imports internal/cli"
			case slices.Contains(leafPkgs, from) && isInternal(to):
				rule = "(b): a leaf imports no internal package"
			}
			if rule == "" {
				continue
			}
			t.Errorf("%s imports %s, breaking rule %s", from, to, rule)
		}
	}
}

// TestLayersMatchCode holds docs/architecture.md's Layers section to what
// go list measures: every internal package named in its proper layer,
// each layer's list exactly matching its measured set, and the header,
// layer, store and CLI counts true. Recounted at 4bee454 by yardr-6dzk:
// 39 packages, previewtest in the leaves, mcp in services, the CLI
// importing 29 of the 39. A package added, removed or moved between layers
// fails here until the page is rewritten with it.
func TestLayersMatchCode(t *testing.T) {
	t.Parallel()
	imports := listImports(t)
	isInternal := func(p string) bool { return strings.HasPrefix(p, internalPrefix) }
	short := func(p string) string { return strings.TrimPrefix(p, internalPrefix) }
	internalOf := func(pkg string) []string {
		var out []string
		for _, to := range imports[pkg] {
			if isInternal(to) {
				out = append(out, to)
			}
		}
		return out
	}
	var internal, leaves []string
	for _, pkg := range slices.Sorted(maps.Keys(imports)) {
		if !isInternal(pkg) {
			continue
		}
		internal = append(internal, pkg)
		if len(internalOf(pkg)) == 0 {
			leaves = append(leaves, pkg)
		}
	}
	otherLayers := map[string]bool{storePkg: true, dispatchPkg: true, runnerPkg: true, serverPkg: true, cliPkg: true}
	leaf := map[string]bool{}
	for _, pkg := range leaves {
		leaf[pkg] = true
	}
	var services []string
	for _, pkg := range internal {
		if !leaf[pkg] && !otherLayers[pkg] {
			services = append(services, pkg)
		}
	}

	raw, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	section := layersSection(t, string(raw))
	// Every internal package is named in the section, backticked as
	// every layer's list names its own.
	for _, pkg := range internal {
		if !strings.Contains(section, "`"+short(pkg)+"`") {
			t.Errorf("the Layers section names no %s", pkg)
		}
	}
	// The header counts every internal package.
	header := ""
	for line := range strings.Lines(section) {
		if strings.TrimSpace(line) != "" {
			header = line
			break
		}
	}
	headerFields := strings.Fields(header)
	if len(headerFields) == 0 {
		t.Fatal("the Layers section has no package count")
	}
	if n, err := strconv.Atoi(headerFields[0]); err != nil || n != len(internal) {
		t.Errorf("the Layers header counts %s, go list says %d internal packages", headerFields[0], len(internal))
	}
	lineFor := func(prefix string) string {
		for line := range strings.Lines(section) {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimRight(line, "\n")
			}
		}
		t.Fatalf("the Layers section has no %q line", prefix)
		return ""
	}
	countOf := func(prefix string, want int) string {
		line := lineFor(prefix)
		if n, ok := parenCount(line); !ok || n != want {
			t.Errorf("the %s line counts %d, go list says %d", prefix, n, want)
		}
		return line
	}
	// Each layer's count, and the leaves and services lists exactly the
	// measured sets.
	leavesLine := countOf("- leaves", len(leaves))
	storeLine := countOf("- store", 1)
	servicesLine := countOf("- services", len(services))
	dispatchLine := countOf("- `dispatch`", 3)
	cliLine := countOf("- cli", 1)
	checkLayer := func(line string, want []string) {
		got := layerNames(line)
		for i, pkg := range want {
			want[i] = short(pkg)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("the page's layer is %v, go list says %v", got, want)
		}
	}
	checkLayer(leavesLine, slices.Clone(leaves))
	checkLayer(storeLine, []string{storePkg})
	checkLayer(servicesLine, slices.Clone(services))
	checkLayer(dispatchLine, []string{dispatchPkg, runnerPkg, serverPkg})
	checkLayer(cliLine, []string{cliPkg})
	// The store's internal-import count and the CLI's imports count.
	f := strings.Fields(storeLine)
	found := false
	for i := 0; i+2 < len(f); i++ {
		if f[i+1] == "internal" && f[i+2] == "imports" {
			found = true
			if n, err := strconv.Atoi(f[i]); err != nil || n != len(internalOf(storePkg)) {
				t.Errorf("the store line counts %s internal imports, go list says %d", f[i], len(internalOf(storePkg)))
			}
		}
	}
	if !found {
		t.Error("the store line states no internal-import count")
	}
	f = strings.Fields(cliLine)
	found = false
	for i := 0; i+5 < len(f); i++ {
		if f[i] == "imports" && f[i+2] == "of" && f[i+3] == "the" && f[i+5] == "internal" {
			found = true
			got, err1 := strconv.Atoi(f[i+1])
			total, err2 := strconv.Atoi(f[i+4])
			if err1 != nil || err2 != nil || got != len(internalOf(cliPkg)) || total != len(internal) {
				t.Errorf("the cli line says imports %s of the %s, go list says %d of the %d",
					f[i+1], f[i+4], len(internalOf(cliPkg)), len(internal))
			}
		}
	}
	if !found {
		t.Error("the cli line states no imports count")
	}
}

// layersSection is the page's Layers section: the lines between its
// heading and the next section's.
func layersSection(t *testing.T, page string) string {
	t.Helper()
	lines := strings.Split(page, "\n")
	start := -1
	for i, line := range lines {
		if line == "## Layers" {
			start = i + 1
		} else if start >= 0 && strings.HasPrefix(line, "## ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	t.Fatal("docs/architecture.md has no ## Layers section")
	return ""
}

// parenCount is the first parenthesised number on a Layers line: the
// layer's count.
func parenCount(line string) (int, bool) {
	for {
		i := strings.Index(line, "(")
		if i < 0 {
			return 0, false
		}
		line = line[i+1:]
		j := strings.Index(line, ")")
		if j < 0 {
			return 0, false
		}
		if n, err := strconv.Atoi(line[:j]); err == nil {
			return n, true
		}
		line = line[j+1:]
	}
}

// layerNames reads a Layers line's package names whether its count comes
// before or after them, and leaves out any explanation after the list.
func layerNames(line string) []string {
	_, list, ok := strings.Cut(line, "): ")
	if !ok || !strings.Contains(list, "`") {
		end := strings.Index(line, " (")
		if end < 0 {
			return nil
		}
		list = line[:end]
	}
	for _, marker := range []string{". (", ". Measured:"} {
		if i := strings.Index(list, marker); i >= 0 {
			list = list[:i]
			break
		}
	}
	parts := strings.Split(list, "`")
	var out []string
	for i := 1; i < len(parts); i += 2 {
		out = append(out, parts[i])
	}
	return out
}
