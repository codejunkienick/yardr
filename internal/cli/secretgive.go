package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/store"
)

// giveSecret is yardr bead answer --secret: past the gate of a weighty card
// (weighed.go), it reads the value of each name the ask asks for as yardr
// source key reads one, unseen from a terminal or as NAME=value lines from
// anything else, and gives them to the yard. What
// it prints is who gave which names and where the credential is set, never
// a value. A login input is read the same way, with no source to read it
// against, and handed to the waiting login. A Google sign-in is read the
// same way again, with no source to read it against, and written to the
// account's credentials file.
func (a *app) giveSecret(c *cobra.Command, st store.API, id string, seq uint64) error {
	secret, err := askedSecret(c.Context(), st, id, seq)
	if err != nil {
		return err
	}
	// Every secret ask is weighty: its gate comes before a value is typed,
	// so a person sees what the card weighs first, and a command with no
	// terminal declines before it reads any.
	weighed, err := a.gate(c, st, id, seq, "give", "--secret")
	if err != nil {
		return err
	}
	if secret.Google != "" {
		values, err := readGoogleKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			fmt.Fprintf(w, "%s gave %s · it is set in %s\n", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
			printReach(w, secret)
		})
	}
	if secret.Hook != "" {
		values, err := readHookKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			fmt.Fprintf(w, "%s gave %s · it is set in %s\n", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
			printReach(w, secret)
		})
	}
	if secret.Login != "" {
		values, err := readLoginKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			fmt.Fprintf(w, "%s gave %s · it went to the waiting login %s, and is kept nowhere\n", a.who(), strings.Join(keyed.Given, " and "), keyed.Login)
		})
	}
	if secret.Harness != "" {
		values, err := readHarnessKeys(secret, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()))
		if err != nil {
			return err
		}
		keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
		if err != nil {
			return secretErr(err)
		}
		return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
			line := fmt.Sprintf("%s gave %s · it is set in %s", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
			if keyed.Missing != "" {
				line += " · " + keyed.Missing
			}
			fmt.Fprintln(w, line)
			if keyed.Next != "" {
				fmt.Fprintln(w, "next: "+keyed.Next)
			}
		})
	}
	src, err := st.GetSource(c.Context(), secret.Source)
	if err != nil {
		return err
	}
	d, err := src.Declaration()
	if err != nil {
		return err
	}
	values, err := readKeys(d, secret.Env, c.InOrStdin(), typedUnseen(c.InOrStdin(), c.ErrOrStderr()), "")
	if err != nil {
		return err
	}
	keyed, err := st.GiveSecret(c.Context(), id, seq, values, a.who(), weighed)
	if err != nil {
		return secretErr(err)
	}
	return a.out(c.OutOrStdout(), keyed, func(w io.Writer) {
		line := fmt.Sprintf("%s gave %s · it is set in %s", a.who(), strings.Join(keyed.Given, " and "), keyed.File)
		if keyed.Missing != "" {
			line += " · " + keyed.Missing
		}
		fmt.Fprintln(w, line)
		if keyed.Next != "" {
			fmt.Fprintln(w, "next: "+keyed.Next)
		}
		printReach(w, secret)
	})
}

// printReach says after a giving what the value reaches and what it does not,
// in the lines the card said before it (store.AskSecret.ReachGiven): a value
// is given for a reason, and the file it is set in is not all it does.
func printReach(w io.Writer, secret *store.AskSecret) {
	for _, line := range secret.ReachGiven() {
		fmt.Fprintln(w, line)
	}
}
