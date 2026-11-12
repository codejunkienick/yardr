package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// generalCardVariable marks a run as the server's carrying out of an
// approved general run card (internal/server/carry_general.go), which
// starts this binary as `--home <home> --actor <approver>` and the
// card's words. Set by hand it only adds a refusal.
const generalCardVariable = "YARDR_GENERAL_CARD"

// generalCommandNamed refuses a general card's carried command when the
// command cobra would run is not named by the card's first words. Cobra
// finds a subcommand past flag words (`yardr bead --json answer` runs
// bead answer, `yardr --json upgrade` runs upgrade), while the store
// reads the command from the first words when it refuses a general card
// (the never-list, the alone match, the specific card's door, in
// internal/store/runask_general.go). A flag among those words would
// carry what the store refused, so the run stops here, before anything
// runs. Off a general card's carrying out it answers nil.
func (a *app) generalCommandNamed(root *cobra.Command, args []string) error {
	if a.env[generalCardVariable] == "" {
		return nil
	}
	if len(args) < 4 || args[0] != "--home" || args[2] != "--actor" {
		return fmt.Errorf("a general card is carried out as --home <home> --actor <approver> and its words, and this run is not")
	}
	words := args[4:]
	cmd, _, err := root.Find(args)
	if err != nil {
		return nil // cobra refuses the words itself, and nothing runs
	}
	path := strings.Fields(cmd.CommandPath())[1:]
	if len(words) < len(path) || !slices.Equal(words[:len(path)], path) {
		return fmt.Errorf("a general card names its command by its first words, with no flag among them: %s would run yardr %s, which the card did not name first. Nothing ran", strings.Join(words, " "), strings.Join(path, " "))
	}
	return nil
}
