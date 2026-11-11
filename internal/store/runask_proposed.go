// What a run ask proposes and what its approval did, as commands and
// clients show them: the stored command whole, the ask's lines for a
// client, and the approval's record. Moved out of runask.go, which stands
// at the file-size ceiling the test gate holds (.yardr/test).
package store

import (
	"strings"
)

// TakenBack is the card's line for the command that takes the change back,
// with who may run it.
func (r AskRun) TakenBack() string { return or(r.BackBy, "anyone") + " can take it back: " + r.Back }

// Command is the command whole, as typed in a terminal; no view shortens it.
func (r AskRun) Command() string { return runCommandLine(r.Argv) }

// CarryPending is what an approval of a pack apply, of an up, of a
// source add that names commands, or of a teammate invite or removal,
// records until the yard's server has carried it out (design
// yardr-bht9 3.5): the ask reads as being carried out while its answer
// holds these words, and as ran or failed once the carrying out
// completes it.
const CarryPending = "approved, to be carried out"

func runCommandLine(argv []string) string { return "yardr " + strings.Join(typedWords(argv), " ") }

// RunAsked is the command a run ask proposes, as a client shows it: the
// command whole, the yard's sentences for what it changes, and the command
// that takes it back. All of it is the yard's, and none of it the asker's.
type RunAsked struct {
	Argv        []string `json:"argv"`
	Command     string   `json:"command"`
	Description string   `json:"description,omitempty"`
	Does        []string `json:"does"`
	Back        string   `json:"back"`
	// BackBy is who may run Back, in words that stand before "can take it
	// back"; absent where that is anyone.
	BackBy string `json:"back_by,omitempty"`
	// Carried is what adding a source takes, for a client to show the
	// texts behind the card; absent on any other command.
	Carried []AskSource `json:"carried,omitempty"`
	// Pack is the pack a pack apply ask names, for a client to show
	// the plan behind the card; absent on any other command.
	Pack *AskPack `json:"pack,omitempty"`
	// Ask are the names the approval gives values for in the clear,
	// one field on the card each; absent on any other command.
	Ask []string `json:"ask,omitempty"`
}

// proposed is the command of the ask a as a client shows it, nil for an
// ask that proposes none.
func (a Ask) proposed() *RunAsked {
	if a.Run == nil {
		return nil
	}
	return &RunAsked{Argv: a.Run.Argv, Command: a.Run.Command(), Description: a.Run.Description,
		Does: a.Run.Does, Back: a.Run.Back, BackBy: a.Run.BackBy, Carried: a.Run.Carried, Pack: a.Run.Pack,
		Ask: a.Run.Ask}
}

// AskApproved is what ApproveAsk did: the ask and who asked it, the command
// as it ran with the yard's sentences for it, and what it printed.
type AskApproved struct {
	Bead   string   `json:"bead"`
	Ask    uint64   `json:"ask"`
	Asker  string   `json:"asker"`
	Argv   []string `json:"argv"`
	Does   []string `json:"does"`
	Result string   `json:"result"`
}

// approvedOn is the ask a change was approved on, as the change's own event
// carries it, so that the events tie the change to what proposed it.
type approvedOn struct {
	Bead string `json:"bead"`
	Ask  uint64 `json:"ask"`
}
