package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A general card's carrying out runs only the command its first words
// name: cobra finds a subcommand past a flag word, and the store read
// the command from the first words when it refused the card, so a flag
// before or among them stops the run before anything runs. Off a
// carrying out the words run as cobra reads them.
func TestGeneralCommandNamed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	carried := map[string]string{generalCardVariable: "1"}
	for _, words := range [][]string{
		{"--json", "upgrade"},
		{"bead", "--json", "answer", "yardr-x", "1"},
		{"settings", "--json", "set", "operator", "yardmaster"},
		{"user", "device", "--json", "add", "ada", "phone"},
	} {
		var errs bytes.Buffer
		args := append([]string{"--home", home, "--actor", "mckean"}, words...)
		if code := execute(&app{env: carried, stderr: &errs}, args); code == 0 || !strings.Contains(errs.String(), "no flag among them") {
			t.Errorf("%v carried: exit %d, %s", words, code, &errs)
		}
	}
	var errs bytes.Buffer
	if code := execute(&app{env: carried, stderr: &errs}, []string{"--home", home, "--actor", "mckean", "bead", "answer", "--help"}); code != 0 {
		t.Errorf("a command named first, carried: exit %d, %s", code, &errs)
	}
	errs.Reset()
	if code := execute(&app{env: carried, stderr: &errs}, []string{"bead", "answer", "--help"}); code == 0 || !strings.Contains(errs.String(), "--home <home> --actor <approver>") {
		t.Errorf("a carried run of another shape: exit %d, %s", code, &errs)
	}
	errs.Reset()
	if code := execute(&app{env: map[string]string{}, stderr: &errs}, []string{"--home", home, "bead", "--json", "answer", "--help"}); code != 0 {
		t.Errorf("off a carrying out: exit %d, %s", code, &errs)
	}
}
