// What a crew member's session reads at prime that the operator and the yard
// set, answered as one read (Prime) for a client that shows it: the role, the
// operator's settings, the memory titles and each depot's rules.
//
// The prime itself is printed by the CLI (yardr prime --crew, primeCrew in
// internal/cli/crew.go), and the store cannot import the CLI, so the reads
// below repeat the ones primeCrew makes: the role file, its First run fold,
// the operator. Each says what it repeats; a change to one is a change to
// the other. The tests of internal/cli hold them together (a prime's role
// and operator against this read's, byte for byte, across the setup's
// states and every style), so a copy that drifts fails there.
package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// PrimeInputs is what Prime answers: the parts of a crew member's prime that
// come from files and settings, not from its own session (its handoff,
// inbox, timers and asks are a prime's rest, and are not here).
type PrimeInputs struct {
	// Role is the member's role file as a session reads it, nil when the
	// member names none.
	Role *PrimeFile `json:"role,omitempty"`
	// Operator is what a prime says of the operator, nil on a yard with
	// neither the style nor the note set.
	Operator *PrimeOperator `json:"operator,omitempty"`
	// Memories are the titles a prime lists, newest first, and
	// MemoriesOmitted how many it leaves out; never a body.
	Memories        []MemoryInfo `json:"memories"`
	MemoriesOmitted int          `json:"memories_omitted"`
	// Depots are the yard's depots, each with the rules files its checkout
	// carries.
	Depots []PrimeDepot `json:"depots"`
}

// PrimeFile is a file a session reads, as it reads it: where it stands and
// what it holds.
type PrimeFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// PrimeOperator is what a prime says about the operator: cli's
// operatorPrime, field for field.
type PrimeOperator struct {
	Name string `json:"name"`
	// Style and Label are the operator_style setting and the answer it
	// stands for; Tells is what it asks of the agent. All three are absent
	// when only the note is set.
	Style string `json:"style,omitempty"`
	Label string `json:"label,omitempty"`
	Tells string `json:"tells,omitempty"`
	Note  string `json:"note,omitempty"`
}

// PrimeDepot is one depot's standing rules for agents: the files named in
// primeRuleFiles that stand at the root of its checkout, in that order.
// Rules is empty, not absent, for a depot with none.
type PrimeDepot struct {
	Name  string      `json:"name"`
	Rules []PrimeFile `json:"rules"`
}

// primeRuleFiles are the files an agent harness reads from a repository's
// root as its rules, in the order they are listed: AGENTS.md and CLAUDE.md.
// .yardr/depot.md is not one: it describes how the yard works the depot, and
// a brief carries it (depotfile.DepotRules).
var primeRuleFiles = []string{"AGENTS.md", "CLAUDE.md"}

// maxPrimeFile bounds a file Prime reads, so that a role or a rules file
// that is not text a session could take cannot fill the server's memory. A
// longer one refuses the call, naming it.
const maxPrimeFile = 1 << 20

// Prime answers what crew's session reads at prime that is the operator's
// and the yard's to set (PrimeInputs). It changes nothing. It is the
// operator's call (rpc.OperatorCalls): a role and a rules file are the
// operator's to read, so on the socket it is the operator's alone, and no
// device is served it at all (rpc.socketOnly). A crew member the yard has
// not is refused as CrewPrimeFor refuses it.
func (s *Store) Prime(ctx context.Context, crew string) (*PrimeInputs, error) {
	r, err := s.GetCrew(ctx, crew)
	if err != nil {
		return nil, err
	}
	out := &PrimeInputs{}
	if out.Role, err = s.primeRole(ctx, r); err != nil {
		return nil, err
	}
	if out.Operator, err = s.primeOperator(ctx); err != nil {
		return nil, err
	}
	if out.Memories, out.MemoriesOmitted, err = s.primeMemories(ctx, crew); err != nil {
		return nil, err
	}
	depots, err := s.Depots(ctx)
	if err != nil {
		return nil, err
	}
	out.Depots = make([]PrimeDepot, 0, len(depots))
	for _, d := range depots {
		rules, err := primeRules(d)
		if err != nil {
			return nil, fmt.Errorf("depot %s: %w", d.Name, err)
		}
		out.Depots = append(out.Depots, PrimeDepot{Name: d.Name, Rules: rules})
	}
	return out, nil
}

// primeRole reads the role of r as primeCrew does: the file its prompt
// names (a relative one under its dir), trimmed, with the First run section
// folded once the setup is done. A member with no prompt has no role, and a
// prompt that cannot be read refuses the read in primeCrew's words, since
// the member's session could not prime either.
func (s *Store) primeRole(ctx context.Context, r *CrewMember) (*PrimeFile, error) {
	path, _ := r.Config["prompt"].(string)
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) && r.Dir != "" {
		path = filepath.Join(r.Dir, path)
	}
	b, err := readPrimeFile(path)
	if err != nil {
		return nil, fmt.Errorf("crew member %s prompt: %w", r.Name, err)
	}
	text := strings.TrimSpace(string(b))
	// The setup's steps are for a yard still being set up. Whether it is
	// takes reads of the whole yard and of its depots' bases, so it is
	// asked only of a role that has a section to fold; one that cannot be
	// read is shown whole, as primeCrew shows it.
	if folded := primeWithoutFirstRun(text); folded != text {
		if done, err := s.setupDone(ctx); err == nil && done {
			text = folded
		}
	}
	return &PrimeFile{Path: path, Text: text}, nil
}

// primeSetupDoneLine is what a prime prints in place of its role's First
// run section once the setup is complete: cli's setupDoneLine.
const primeSetupDoneLine = "The setup is done (`yardr doctor`); the steps are in `yardr docs setup`, should a depot be added."

// primeWithoutFirstRun is cli's withoutFirstRun: the role with its "## First
// run" section, heading and all, replaced by primeSetupDoneLine, and a role
// without that heading as it is.
func primeWithoutFirstRun(role string) string {
	lines := strings.Split(role, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## First run" {
			start = i
			break
		}
	}
	if start < 0 {
		return role
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	out := slices.Clone(lines[:start])
	if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) != "" {
		out = append(out, "")
	}
	out = append(out, primeSetupDoneLine)
	if end < len(lines) {
		out = append(out, "")
		out = append(out, lines[end:]...)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// setupDone reports whether the yard's setup is complete: cli's nextStep of
// its setupSteps is nil. The steps are read there from this same store
// (setup.go), but they are a command's report, whose prose names the packs
// (internal/pack imports the store, so the store cannot call it); what a
// role's fold asks is only whether every step the setup waits for is done,
// which is this. A yard with no depot and no work that needs one is set up
// with none; with a depot, every depot has a flow (followed in the store, or
// found in its files), the stages work starts and ends at have a route
// (approved too where a depot lands its work in a repository), and a bead
// of work has gone through to the end of its flow, which a first bead
// implies.
func (s *Store) setupDone(ctx context.Context) (bool, error) {
	depots, err := s.Depots(ctx)
	if err != nil {
		return false, err
	}
	beads, err := s.ListBeads(ctx, BeadFilter{All: true})
	if err != nil {
		return false, err
	}
	if len(depots) == 0 {
		return !slices.ContainsFunc(beads, func(b Bead) bool { return b.Depot == nil && !NotWork[b.Type] }), nil
	}
	pointers, err := s.FlowPointers(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range depots {
		if slices.ContainsFunc(pointers, func(p FlowPointer) bool { return p.Type == "" && p.Depot == d.Name && p.Flow != "" }) {
			continue // its flow is the store's, not its base's
		}
		// A base that does not resolve is a step not done, not a failure.
		if rf, err := s.RepoFlows(ctx, d.Name); err != nil || len(rf.Flows) == 0 {
			return false, nil
		}
	}
	routes, err := s.Routes(ctx)
	if err != nil {
		return false, err
	}
	stages := []string{"new", "review"}
	if slices.ContainsFunc(depots, func(d Depot) bool { return !d.IsDir() }) {
		stages = append(stages, "approved")
	}
	for _, stage := range stages {
		if !slices.ContainsFunc(routes, func(r Route) bool { return r.Stage == stage }) {
			return false, nil
		}
	}
	return slices.ContainsFunc(beads, func(b Bead) bool {
		return b.Depot != nil && !NotWork[b.Type] && b.Status == "closed" && b.CloseReason != nil && *b.CloseReason == b.Stage
	}), nil
}

// primeStyles are the answers of operator_style as a prime tells them, from
// the most explaining to the least: cli's workStyles, without the words its
// form shows. The keys are OperatorStyles'.
var primeStyles = []struct{ key, label, tells string }{
	{"guide", "Guide me",
		"Explain each step and why, in plain words, before you take it. Show no raw commands unless asked: " +
			"say what a command does instead. Ask before anything you do on their behalf."},
	{"show", "Show me",
		"Explain briefly what you do. Show the command you run. Ask when there is a decision to make."},
	{"work", "Work with me",
		"Be terse. Assume they read code and git: no explaining of either. Decide small things yourself " +
			"and say what you decided."},
	{"report", "Just report",
		"Decide what is yours to decide. Lead with the result, then how you got there, if at all. Come to them " +
			"only for decisions that are theirs."},
}

// primeOperator reads what a prime says about the operator, as cli's
// primeOperator does: nil on a yard with neither setting, and a stored style
// this build does not know (a newer build's) passed over rather than
// guessed at.
func (s *Store) primeOperator(ctx context.Context) (*PrimeOperator, error) {
	all, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var style struct{ key, label, tells string }
	known := false
	for _, st := range primeStyles {
		if st.key == all[StyleSetting] {
			style, known = st, true
			break
		}
	}
	note := strings.TrimSpace(all[NoteSetting])
	if !known && note == "" {
		return nil, nil
	}
	return &PrimeOperator{Name: or(all[OperatorSetting], defaultOperatorName()),
		Style: style.key, Label: style.label, Tells: style.tells, Note: note}, nil
}

// defaultOperatorName is who a prime names as the operator on a yard whose
// operator setting was never written: cli's defaultOperator without its last
// step. That is the OS's own name for the login, through config's bounded
// lookup, which the store may not import; what is left is the first of $USER
// and $LOGNAME that is a name a user may have, else "operator". A server's
// environment carries neither, so for such a yard the read says "operator"
// where its CLI names the login. Only a yard that never ran yardr init is one:
// init writes the setting.
func defaultOperatorName() string {
	for _, name := range []string{os.Getenv("USER"), os.Getenv("LOGNAME")} {
		if ValidUserName(name) {
			return name
		}
	}
	return "operator"
}

// primeMemories are the memory titles a prime lists for the crew member
// name, as CrewPrimeFor lists them: the most recently written PrimeMemories
// of its scope, newest first, and how many that leaves out. Titles only.
func (s *Store) primeMemories(ctx context.Context, name string) ([]MemoryInfo, int, error) {
	mems, err := s.Recall(ctx, "crew:"+name, "")
	if err != nil {
		return nil, 0, err
	}
	slices.SortFunc(mems, func(a, b MemoryInfo) int {
		return cmp.Or(b.UpdatedAt.Compare(a.UpdatedAt), strings.Compare(a.ID, b.ID))
	})
	omitted := 0
	if len(mems) > PrimeMemories {
		omitted, mems = len(mems)-PrimeMemories, mems[:PrimeMemories]
	}
	if mems == nil {
		mems = []MemoryInfo{}
	}
	return mems, omitted, nil
}

// primeRules are the rules files at the root of the depot's checkout
// (primeRuleFiles), those that stand there as regular files: a depot with
// none, or whose checkout is gone, has no rules, which is no error. A
// symlink is followed only while it stays inside the checkout, since a
// file a depot's history put there is not the operator's to be shown from
// outside it. A file that stands and cannot be read is an error, so the
// operator is not shown a depot with fewer rules than its agents read.
func primeRules(d Depot) ([]PrimeFile, error) {
	rules := []PrimeFile{}
	// An empty path would read the server's own working directory.
	if d.Path == "" {
		return rules, nil
	}
	root, err := filepath.EvalSymlinks(d.Path)
	if err != nil {
		return rules, nil
	}
	for _, name := range primeRuleFiles {
		real, err := filepath.EvalSymlinks(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(rel) {
			continue
		}
		b, err := readPrimeFile(real)
		if errors.Is(err, errPrimeNotFile) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rules = append(rules, PrimeFile{Path: filepath.Join(d.Path, name), Text: string(b)})
	}
	return rules, nil
}

// errPrimeNotFile says what stands at a path is no regular file.
var errPrimeNotFile = errors.New("not a regular file")

// readPrimeFile reads the regular file at path whole, up to maxPrimeFile.
// It is opened without blocking and judged by the open file, so a path that
// became a pipe cannot hold the read.
func readPrimeFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, errPrimeNotFile)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxPrimeFile+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) > maxPrimeFile {
		return nil, fmt.Errorf("%s is longer than %d bytes", path, maxPrimeFile)
	}
	return b, nil
}
