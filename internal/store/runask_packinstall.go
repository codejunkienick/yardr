// The pack install row of the run-ask list: the programs a pack's
// declarations name under needs that this machine lacks, installed
// when the operator approves. Installing is a program run, which no
// store write does, so the approval is phased as a call is
// (runask_call.go): the approval is held to the card in one write,
// the installs run with no write open through the source's install
// seam, and the answer is recorded in another. An install that
// fails leaves the ask open, and approving again retries it.
//
// The card shows, per missing program, the program, the pack it is
// from, and the install file's text whole with its sha256, as a
// source add card shows its command files: approving runs exactly
// the bytes the card showed, held by Seen. An install that names an
// absolute program is the operator's own and stays in a terminal.
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// packInstallUse is how a refusal names the pack install a run ask takes.
const packInstallUse = "yardr pack install <pack>"

// packInstallDescription is the command's Short in internal/cli, checked
// there so the card keeps the help the person would see at a terminal.
const packInstallDescription = "Install the programs a pack declares it needs that this machine lacks"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"pack", "install"},
		use:         packInstallUse,
		description: packInstallDescription,
		parse:       parsePackInstall,
		check:       checkPackInstall,
		card:        (*wtx).packInstallCard,
		stale:       (*wtx).packInstallStale,
		weigh:       packInstallWeight,
		run:         runPackInstall,
	})
}

// parsePackInstall reads the words of pack install <pack>: one pack.
func parsePackInstall(rest []string) ([]string, error) {
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one pack, not %d words", packInstallUse, len(rest))
	}
	return []string{rest[0]}, nil
}

// checkPackInstall is installing a pack's programs' own check as the
// asker: it is the operator's, as applying the pack is, proposable
// where the server registered its reading of the packs.
func checkPackInstall(ctx context.Context, w *wtx, argv []string, actor string) error {
	if w.packReaderOf() == nil {
		return fmt.Errorf("pack %s: no server has registered its reading of the packs yardr embeds on this store, so no card can be written from the binary that runs the yard: a pack is proposed from a yard its server runs", argv[2])
	}
	return w.may(ctx, actor, ActApplyPack, Object{Kind: KindPack, Name: argv[2], Ask: Askable})
}

// resolvedNeed is one missing program of a pack, read for a card or
// an approval: the declaration that names it, the need, and the
// install file it carries, nil where the install names an absolute
// program of the operator's own.
type resolvedNeed struct {
	decl *source.Declaration
	need source.Need
	file *AskFile
}

// packInstallNeeds reads the pack argv names as a setup card reads it
// (setupReading: the embedded pack, else the one the operator applied
// from a directory) and answers its missing programs, each once, in
// the pack's declaration order. carried is every source as the store
// holds it, for the card's fingerprint of the pack's bytes.
func (w *wtx) packInstallNeeds(pack string) (*PackReading, []AskSource, []resolvedNeed, error) {
	reading, err := w.setupReading(pack)
	if err != nil {
		return nil, nil, nil, err
	}
	if reading == nil || reading.Name != pack {
		return nil, nil, nil, fmt.Errorf("pack %s: the server's reading named another pack: nothing was stored", pack)
	}
	carried := make([]AskSource, 0, len(reading.Sources))
	decls := map[string]*source.Declaration{}
	for _, s := range reading.Sources {
		d, norm, err := validatePackSource(s, pack)
		if err != nil {
			return nil, nil, nil, err
		}
		decls[d.Name] = d
		carried = append(carried, norm)
	}
	var out []resolvedNeed
	seenProg := map[string]bool{}
	for i := range carried {
		d := decls[carried[i].Name]
		for _, n := range d.MissingNeeds() {
			if seenProg[n.Program] {
				continue
			}
			seenProg[n.Program] = true
			rn := resolvedNeed{decl: d, need: n}
			if !strings.HasPrefix(n.Install[0], "/") {
				for j := range carried[i].Files {
					if carried[i].Files[j].Program == n.Install[0] {
						f := carried[i].Files[j]
						rn.file = &f
						break
					}
				}
				if rn.file == nil {
					return nil, nil, nil, fmt.Errorf("source %s: its declaration names the install file %s of %s, and the pack carries no bytes for it", d.Name, n.Install[0], n.Program)
				}
			}
			out = append(out, rn)
		}
	}
	return reading, carried, out, nil
}

// declaredNeeds counts the programs a pack's declarations name under
// needs, each once: what a card with nothing missing is refused for.
func declaredNeeds(carried []AskSource) (int, error) {
	seenProg := map[string]bool{}
	n := 0
	for _, s := range carried {
		d, err := source.Parse("pack source "+s.Name, []byte(s.Text))
		if err != nil {
			return 0, fmt.Errorf("%s", OneLine(err.Error()))
		}
		for _, need := range d.Needs {
			if !seenProg[need.Program] {
				seenProg[need.Program] = true
				n++
			}
		}
	}
	return n, nil
}

// packInstallCard is the card of pack install <pack>: per missing
// program, the program, its pack, and its install file's text whole
// with its sha256. Asking refuses a pack the server does not read, a
// pack that declares nothing missing, and an install that names an
// absolute program, which stays in a terminal. An approval reads the
// card again without that, and says in its own words what changed
// (packInstallStale).
func (w *wtx) packInstallCard(argv []string, asking bool) (AskRun, error) {
	card, _, err := w.packInstallCardNeeds(argv, asking)
	return card, err
}

// packInstallCardNeeds builds the pack install card and returns the
// resolved needs from that single reading, so preparePackInstall
// verifies and stands the exact bytes read once without a second read
// from disk.
func (w *wtx) packInstallCardNeeds(argv []string, asking bool) (AskRun, []resolvedNeed, error) {
	pack := argv[2]
	reading, carried, needs, err := w.packInstallNeeds(pack)
	if err != nil {
		return AskRun{}, nil, err
	}
	seenMap := map[string]string{
		"pack " + pack:            reading.Label,
		"pack " + pack + " build": reading.Build,
		"pack " + pack + " bytes": packBytesSeen(&PackReading{Sources: carried}),
	}
	back := "an installed program is not removed by the yard"
	if len(needs) == 0 {
		if !asking {
			return AskRun{Argv: argv, Does: []string{fmt.Sprintf("every program pack %s needs is on this machine now.", pack)},
				Back: back, BackBy: "nobody", Seen: seenMap}, nil, nil
		}
		declared, err := declaredNeeds(carried)
		if err != nil {
			return AskRun{}, nil, err
		}
		if declared == 0 {
			return AskRun{}, nil, fmt.Errorf("pack %s declares no program it needs: its sources name no needs, so there is nothing to install", pack)
		}
		return AskRun{}, nil, fmt.Errorf("pack %s needs nothing this machine lacks: every program it names is here, so there is nothing to install", pack)
	}
	var does []string
	totalText := 0
	for _, rn := range needs {
		env := ""
		if rn.need.Env != "" {
			env = fmt.Sprintf(" (%s names no program to run either)", rn.need.Env)
		}
		does = append(does, safeSourceCardLine(fmt.Sprintf("pack %s (%s) needs %s, which is not on this machine%s.", pack, reading.Label, rn.need.Program, env)))
		switch {
		case rn.file == nil:
			if asking {
				return AskRun{}, nil, fmt.Errorf("pack %s: the install of %s names the absolute program %s, which is the operator's own to have installed: install it with %s in a terminal", pack, rn.need.Program, rn.need.Install[0], packInstallUse)
			}
			does = append(does, safeSourceCardLine(fmt.Sprintf("the install of %s names an absolute program now.", rn.need.Program)))
			seenMap["need "+rn.need.Program] = seen(rn.need.Program, "absolute")
		case len(rn.file.Text) > MaxRunSourceCommandFile:
			return AskRun{}, nil, fmt.Errorf("pack %s: the install file %s of %s is %d bytes: a card holds at most %d, so this is the operator's to type in a terminal (%s)", pack, rn.file.Program, rn.need.Program, len(rn.file.Text), MaxRunSourceCommandFile, packInstallUse)
		case strings.IndexFunc(rn.file.Text, crewRoleHidden) >= 0 || strings.Contains(rn.file.Text, NoteFilesMark) || strings.Contains(rn.file.Text, WeightMark):
			return AskRun{}, nil, fmt.Errorf("pack %s: the install file %s of %s holds control characters or a mark the yard draws on a card itself, which a card would not show as written: install it with %s in a terminal", pack, rn.file.Program, rn.need.Program, packInstallUse)
		default:
			totalText += len(rn.file.Text)
			if totalText > MaxRunSourceTexts {
				return AskRun{}, nil, fmt.Errorf("pack %s: its install files run to %d bytes together: a card holds at most %d, so this is the operator's to type in a terminal (%s)", pack, totalText, MaxRunSourceTexts, packInstallUse)
			}
			does = append(does, safeSourceCardLine(fmt.Sprintf("the install file %s (sha256 %s, %d bytes) is run as %s; its text whole:", rn.file.Program, rn.file.SHA256, len(rn.file.Text), showCommand(rn.need.Install))))
			does = append(does, strings.Split(strings.TrimRight(rn.file.Text, "\n"), "\n")...)
			seenMap["need "+rn.need.Program] = seen(rn.need.Program, rn.need.Install, rn.file.SHA256)
		}
	}
	return AskRun{Argv: argv, Does: does, Back: back, BackBy: "nobody", Seen: seenMap}, needs, nil
}

// packInstallStale says why the ask seq for pack install is approved
// no more: a program the card named was installed since, or its
// install's bytes are not the bytes the card showed.
func (w *wtx) packInstallStale(seq uint64, was, now AskRun) error {
	pack := was.Argv[2]
	for key, wasSeen := range was.Seen {
		program, isNeed := strings.CutPrefix(key, "need ")
		if !isNeed {
			continue
		}
		nowSeen, ok := now.Seen[key]
		switch {
		case !ok:
			return fmt.Errorf("ask %d was written while %s was missing for pack %s: it is installed since. %s", seq, program, pack, nothingRan)
		case nowSeen != wasSeen:
			return fmt.Errorf("ask %d was written for another install of %s for pack %s: its bytes changed since, and changed bytes are a new card. %s", seq, program, pack, nothingRan)
		}
	}
	return staleCard(seq, was, now)
}

// packInstallWeight weighs a pack install card: code, a program the
// yard then runs, whole on the card.
func packInstallWeight(w *wtx, run AskRun) ([]AskWeight, error) {
	var programs []string
	for key := range run.Seen {
		if program, ok := strings.CutPrefix(key, "need "); ok {
			programs = append(programs, program)
		}
	}
	slices.Sort(programs)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("the install of %s for pack %s, a program the yard then runs", strings.Join(programs, ", "), run.Argv[2]))}, nil
}

// runPackInstall refuses: installing is a program run, which no
// store write does, so its approval runs in phases outside one (see
// approvePackInstall), as a call's does.
func runPackInstall(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	return "", fmt.Errorf("pack install %s: the yard runs it itself, and its approval is not this write", argv[2])
}

// packInstallTarget reports whether ask seq on bead id is a pack
// install, for ApproveAsk to run in phases. A bead or ask that is
// not there, and any other command, is the plain approval's to
// refuse or run.
func (s *Store) packInstallTarget(id string, seq uint64) bool {
	found := false
	_ = s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, id)
		if err != nil {
			return nil
		}
		notes, err := beadNotes(tx, row.UUID)
		if err != nil {
			return nil
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.Note.Seq != seq || r.AskErr != nil || r.Ask.Run == nil {
				continue
			}
			if _, argv, err := parseRun(r.Ask.Run.Argv); err == nil && len(argv) >= 3 && argv[0] == "pack" && argv[1] == "install" {
				found = true
			}
		}
		return nil
	})
	return found
}

// packInstallJob is one phased install between its writes: what the
// card held, and each install stood as it is run.
type packInstallJob struct {
	asker   string
	argv    []string
	does    []string
	pack    string
	decls   []*source.Declaration
	needs   []source.Need
	argvs   [][]string
	cleanup []func()
}

// approvePackInstall is ApproveAsk for a pack install, in three
// parts no one write holds: first the approval is held to the card,
// then each install runs with no store write open, then the answer
// is recorded. An install that fails leaves the ask open, saying
// what failed, and approving again retries it.
func (s *Store) approvePackInstall(ctx context.Context, id string, seq uint64, actor string, weighed bool) (*AskApproved, error) {
	var job *packInstallJob
	err := s.writeVia(ctx, func(w *wtx) (err error) {
		job, err = w.preparePackInstall(ctx, id, seq, actor, weighed)
		return err
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, clean := range job.cleanup {
			clean()
		}
	}()
	var programs []string
	for i, n := range job.needs {
		if err := source.InstallNeed(ctx, job.decls[i], n, job.argvs[i]); err != nil {
			if len(programs) > 0 {
				return nil, fmt.Errorf("installing %s for pack %s failed: %v (installed %s before it failed: ask a new card for the rest)", n.Program, job.pack, err, strings.Join(programs, ", "))
			}
			return nil, fmt.Errorf("installing %s for pack %s failed: %v. Approve again to retry", n.Program, job.pack, err)
		}
		programs = append(programs, n.Program)
	}
	result := fmt.Sprintf("pack %s: installed %s", job.pack, strings.Join(programs, ", "))
	var out *AskApproved
	err = s.writeVia(ctx, func(w *wtx) error {
		var err error
		out, err = w.finishPhasedRun(ctx, id, seq, actor, job.asker, job.argv, job.does, result)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// preparePackInstall is the first write of approvePackInstall: as
// approveAsk checks, it holds the approval to the card the yard
// would write now, weighs it again, and applies the weighed and
// strong gates. It writes nothing the install changes: each install
// is stood from the card's bytes here and run between the writes.
func (w *wtx) preparePackInstall(ctx context.Context, id string, seq uint64, actor string, weighed bool) (*packInstallJob, error) {
	who, err := paneAuthor(w.Tx, callerOf(ctx), actor)
	if err != nil {
		return nil, err
	}
	actor = who
	row, err := w.loadBead(id)
	if err != nil {
		return nil, err
	}
	notes, err := beadNotes(w.Tx, row.UUID)
	if err != nil {
		return nil, err
	}
	var r *ResolvedAsk
	resolved := ResolveAsks(notes, now())
	for i := range resolved {
		if resolved[i].Note.Seq == seq {
			r = &resolved[i]
		}
	}
	switch {
	case r == nil && slices.ContainsFunc(notes, func(n Note) bool { return n.Seq == seq }):
		return nil, fmt.Errorf("note %d on bead %s is not an ask", seq, row.ID)
	case r == nil:
		return nil, fmt.Errorf("bead %s has no ask %d", row.ID, seq)
	case r.AskErr != nil:
		return nil, r.AskErr
	case r.Ask.Run == nil:
		return nil, fmt.Errorf("ask %d proposes no command to approve: it is answered with yardr bead answer %s %d <n> [words], or --words", seq, row.ID, seq)
	case r.State == AskDeclined:
		return nil, fmt.Errorf("ask %d was declined: %s", seq, declinedLine(*r.Current))
	case r.State == AskTakenBack:
		return nil, fmt.Errorf("ask %d is asked no more: %s", seq, takenBackLine(*r.Current))
	case r.State == AskAnswered:
		return nil, fmt.Errorf("ask %d was approved already, by %s: a command runs once for one ask", seq, r.Current.Note.Author)
	}
	was := *r.Ask.Run
	c, argv, err := parseRun(was.Argv)
	if err != nil {
		return nil, fmt.Errorf("ask %d: %w", seq, err)
	}
	if err := w.may(ctx, actor, ActApproveRun, Object{Kind: KindAsk, Name: row.ID, Seq: seq, To: r.Ask.To}); err != nil {
		return nil, err
	}
	if !slices.Equal(argv, was.Argv) {
		return nil, fmt.Errorf("ask %d holds a command the yard did not write (%s): nothing ran", seq, was.Command())
	}
	card, needs, err := w.packInstallCardNeeds(argv, false)
	if err != nil {
		return nil, fmt.Errorf("ask %d: %w. %s", seq, err, nothingRan)
	}
	if !sameMap(card.Seen, was.Seen) || !slices.Equal(card.Does, was.Does) || card.Back != was.Back || card.BackBy != was.BackBy {
		return nil, w.packInstallStale(seq, was, card)
	}
	weight, err := w.weighRun(c, card)
	if err != nil {
		return nil, fmt.Errorf("ask %d: %w. %s", seq, err, nothingRan)
	}
	if err := weightStale(seq, r.Ask.Weight, weight); err != nil {
		return nil, err
	}
	if err := w.gateRefusal(seq, weight, r.Ask.Weight, actor, weighed); err != nil {
		return nil, err
	}
	job := &packInstallJob{asker: r.Note.Author, argv: argv, does: was.Does, pack: argv[2]}
	ok := false
	defer func() {
		if !ok {
			for _, clean := range job.cleanup {
				clean()
			}
		}
	}()
	for _, rn := range needs {
		if rn.file == nil {
			return nil, fmt.Errorf("ask %d: the install of %s names an absolute program. %s", seq, rn.need.Program, nothingRan)
		}
		argvFor, cleanup, err := source.InstallArgv(rn.need, []byte(rn.file.Text))
		if err != nil {
			return nil, fmt.Errorf("ask %d: %w. %s", seq, err, nothingRan)
		}
		job.decls = append(job.decls, rn.decl)
		job.needs = append(job.needs, rn.need)
		job.argvs = append(job.argvs, argvFor)
		job.cleanup = append(job.cleanup, cleanup)
	}
	ok = true
	return job, nil
}
