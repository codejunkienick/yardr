// The group set row of the run list (runList): a group's prompt, an exec
// group's command or a permission preset that never asks, changed on a card
// and set only on the operator's approval (design yardr-m90n 3.5). What a
// later session of the group runs is what the change sets, so none of it
// is told after it runs: the delegated leg refuses all three. The card shows
// each whole, what stands now and what will, and is weighed code. The words
// ride as the terminal's own, so the card's command pastes into a terminal,
// except the preset, which a terminal reads into the kind's flags and the
// card names by its label (runask_strong.go). A prompt is a file under the
// yard's home as group add's is: its path rides argv and its text rides
// beside it (AskRun.Role), held to the file at the card and again at the
// approval, which writes the text to a file of the yard's own and sets that
// (runask_promptfile.go). Every other setting of a group is not this card's,
// and refuses beside it in words that say where it goes instead.
//
// The row is registered here, beside its functions, as the peer set row is
// (runask_peerset.go), and not in runList's literal: runask.go stands at the
// file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
)

// groupSetUse is how a refusal names the group set row.
const groupSetUse = "yardr group set <name> prompt=<file> | cmd=<command> | preset=<preset>"

// groupSetKeys are the settings the row carries, in the order its stored
// form writes them.
var groupSetKeys = []string{"prompt", "cmd", "preset"}

func init() {
	runList = append(runList, runCommand{
		words:       []string{"group", "set"},
		use:         groupSetUse,
		description: "Change some settings of a group and keep the rest",
		parse:       parseGroupSet,
		checkRun:    checkGroupSet,
		cardRun:     (*wtx).groupSetCard,
		stale:       (*wtx).groupSetStale,
		weigh:       groupSetWeight,
		runRun:      runGroupSet,
	})
}

// parseGroupSet reads the words of group set <name> that carry the strong
// few. It reads no record of the yard. Words that carry none are not this
// row's: the other settings of a group are the operator's alone, or the
// yardmaster's for the operator, and askedRun refuses them in those words.
func parseGroupSet(rest []string) ([]string, error) {
	name, sets, err := strongSetWords(groupSetUse, "group set", "group", groupSetKeys, rest)
	if err != nil {
		return nil, err
	}
	return strongSetArgv(name, sets, groupSetKeys), nil
}

// groupSetSpec is a group set proposal as the yard holds it: the group as it
// stands and as the approval would leave it, the config keys the approval
// writes, the prompt text its file was held to, and the preset resolved to
// the flags of the kind the group starts.
type groupSetSpec struct {
	name   string
	have   Group
	want   Group
	set    map[string]any
	prompt string
	kind   string
	preset Permission
}

// groupSetProposal holds a group set ask to the yard as it stands: the group
// is there, a prompt is a file under the yard's home that holds the text the
// ask carries, a preset is one of its kind's that never asks, and the config
// that results is one the store would save (configProblems, as putGroup holds
// it), so a command on a runner that reads none of it is refused here in the
// store's words.
func (w *wtx) groupSetProposal(run AskRun) (groupSetSpec, error) {
	name, sets := strongSetOf(run.Argv)
	spec := groupSetSpec{name: name, set: map[string]any{}}
	if ok, err := get(w.Tx, bGroups, name, &spec.have); err != nil {
		return groupSetSpec{}, err
	} else if !ok {
		return groupSetSpec{}, fmt.Errorf("group %s: %w", name, ErrNotFound)
	}
	subject := "group " + name
	if path, ok := sets["prompt"]; ok {
		saved, err := w.heldPrompt(name, subject, path, run.Role, promptWords{
			otherwise: "the prompt is set in a terminal, with yardr group set " + name + " prompt=<file>", spelled: "prompt=<path>"})
		if err != nil {
			return groupSetSpec{}, err
		}
		spec.set["prompt"], spec.prompt = saved, run.Role
	} else if run.Role != "" {
		return groupSetSpec{}, fmt.Errorf("%s: the ask carries prompt text but names no prompt: name the file it was read from as prompt=<path>", subject)
	}
	if cmd, ok := sets["cmd"]; ok {
		spec.set["cmd"] = cmd
	}
	if label, ok := sets["preset"]; ok {
		kind, err := w.kindOf(spec.have.Config)
		if err != nil {
			return groupSetSpec{}, err
		}
		p, err := strongPreset(subject, kind, label)
		if err != nil {
			return groupSetSpec{}, err
		}
		args := make([]any, len(p.Args))
		for i, a := range p.Args {
			args[i] = a
		}
		spec.kind, spec.preset, spec.set["args"] = kind, p, args
	}
	spec.want = spec.have
	spec.want.Config = maps.Clone(spec.have.Config)
	if spec.want.Config == nil {
		spec.want.Config = map[string]any{}
	}
	maps.Copy(spec.want.Config, spec.set)
	if ps := configProblems(GroupConfig, name, spec.have.Runner, spec.want.Config, spec.have.Config); len(ps) > 0 {
		return groupSetSpec{}, ps[0]
	}
	return spec, nil
}

// checkGroupSet is setting a group as the asker: what defines what the yard
// runs is the operator's, which a run ask may propose. The proposal is held
// first, so a prompt, preset or config the yard would refuse is refused in
// its own words whoever asks. The gate then is the store's own
// (checkGroupDefine), which marks what it holds to the operator alone: this
// row is what proposes it, so the mark is changed here and nowhere else.
func checkGroupSet(ctx context.Context, w *wtx, run AskRun, actor string) error {
	spec, err := w.groupSetProposal(run)
	if err != nil {
		return err
	}
	// The gate lets a change of nothing through as it lets anyone's, which
	// would read as a command to run yourself.
	if reflect.DeepEqual(asStored(spec.have.Config), asStored(spec.want.Config)) {
		return fmt.Errorf("group %s is set so already, so the command would change nothing", spec.name)
	}
	err = checkGroupDefine(ctx, w, spec.have, spec.want, actor)
	var o *OperatorOnlyError
	if errors.As(err, &o) {
		return &OperatorOnlyError{msg: o.msg, ask: Askable}
	}
	return err
}

// groupSetFingerprint is what a card of the strong few rests on of a group:
// the runner and the settings the card says something of.
func groupSetFingerprint(g Group) string {
	c := asStored(g.Config)
	return seen(g.Runner, c["prompt"], c["cmd"], c["kind"], c["args"])
}

// groupSetCard is the card of group set <name>: the group, then for each
// setting the card carries what stands now and what the approval sets, each
// whole. Asking refuses a proposal the yard would refuse and one that would
// change nothing (checkGroupSet); an approval reads it again, and stale says
// what moved. The card holds the group's fingerprint and the new prompt's
// SHA-256 in Seen, and the prompt text in Role.
func (w *wtx) groupSetCard(run AskRun, _ bool) (AskRun, error) {
	spec, err := w.groupSetProposal(run)
	if err != nil {
		return AskRun{}, err
	}
	_, sets := strongSetOf(run.Argv)
	does := []string{fmt.Sprintf("changes the group %s on the %s runner: what each of its sessions runs, as the lines below say, and nothing else of it.", spec.name, spec.have.Runner)}
	seenMap := map[string]string{"group " + spec.name: groupSetFingerprint(spec.have)}
	if _, ok := sets["prompt"]; ok {
		was, _ := spec.have.Config["prompt"].(string)
		path, _ := spec.set["prompt"].(string)
		does = append(does, w.promptLines(was, path, spec.prompt)...)
		sum := sha256.Sum256([]byte(spec.prompt))
		seenMap["prompt "+path] = hex.EncodeToString(sum[:])
	}
	if cmd, ok := spec.set["cmd"].(string); ok {
		if was, _ := spec.have.Config["cmd"].(string); was != "" {
			does = append(does, fmt.Sprintf("Each session runs sh -c with %s now.", strconv.Quote(was)))
		} else {
			does = append(does, "It runs no command now.")
		}
		does = append(does, fmt.Sprintf("Each session will run sh -c with %s, as its program stands when the session starts.", strconv.Quote(cmd)))
	}
	if _, ok := sets["preset"]; ok {
		does = append(does, presetLines(spec.kind, spec.kind, spec.have.Config["args"], spec.preset)...)
	}
	return AskRun{
		Argv:   run.Argv,
		Does:   does,
		Back:   strongBack("group set", spec.name, spec.have.Config, spec.set),
		BackBy: "only the operator",
		Seen:   seenMap,
		Role:   spec.prompt,
	}, nil
}

// groupSetStale says why the ask seq for group set is approved no more: the
// group was set since, or the yard would not write its card as it stands now.
func (w *wtx) groupSetStale(seq uint64, was, now AskRun) error {
	name := was.Argv[2]
	if key := "group " + name; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "group_set", naming("name", name))
		return fmt.Errorf("ask %d was written when the group %s was another: it was set since%s. %s", seq, name, since(e), nothingRan)
	}
	return staleCard(seq, was, now)
}

// groupSetWeight weighs a group set card: its sessions run a program the
// card shows, the group's prompt, command or the flags that never ask.
func groupSetWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	name, sets := strongSetOf(run.Argv)
	return []AskWeight{weighs(WeightCode, fmt.Sprintf("%s of the group %s, which each session the yard starts for it then runs as the card shows", strings.Join(strongChanged(sets), " and "), name))}, nil
}

// runGroupSet sets the group as actor, through the store function the
// terminal's group set calls (setGroup), so the checks that function makes
// are the same: the approver is the operator, who may define a group. The
// proposal is held once more in this write, so what is saved is what the
// card showed. It answers the lines the command prints for what changed.
func runGroupSet(ctx context.Context, w *wtx, run AskRun, actor string, _ *approvedOn) (string, error) {
	spec, err := w.groupSetProposal(run)
	if err != nil {
		return "", err
	}
	ch, err := w.setGroup(ctx, spec.name, GroupEdit{Set: spec.set}, actor, nil)
	if err != nil {
		return "", err
	}
	if path, ok := spec.set["prompt"].(string); ok {
		if err := w.writePrompt("group "+spec.name, path, spec.prompt); err != nil {
			return "", err
		}
	}
	lines := []string{fmt.Sprintf("group %s saved", spec.name)}
	for _, key := range []string{"args", "cmd", "prompt"} {
		was, had := ch.Was.Config[key]
		is, has := ch.Is.Config[key]
		if a, b := grantSettingText(was, had), grantSettingText(is, has); had != has || a != b {
			lines = append(lines, fmt.Sprintf("  %s: %s -> %s", key, or(a, "(not set)"), or(b, "(dropped)")))
		}
	}
	return strings.Join(lines, "\n"), nil
}
