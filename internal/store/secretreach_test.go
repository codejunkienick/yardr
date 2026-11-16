package store

import (
	"strings"
	"testing"
)

// A secret ask's value is said to reach what its kind reaches: where it lands,
// which reads use it, and which do not, in the lines a card carries and the
// answer prints. Each kind says its own, and a harness key and a waiting
// login's input, whose card line already says where they go, add none.
func TestSecretReach(t *testing.T) {
	t.Parallel()
	const mint = "mint: where the source mints, these names are its inputs; a static token beside them is left as it is, and not used while they are set"
	for _, c := range []struct {
		name string
		s    AskSecret
		want []string
	}{
		{"a source's credential", AskSecret{Source: "crm"}, []string{
			"lands in: the source's env_file, or sources/crm.env in the yard's home when it has none",
			"read by: declared calls (yardr call crm ...) and the source's check, from the file at each call",
			"not reached: thread commands (yardr reply, thread open, thread state), which read the speaker's own file; a crew member's is given with --for-crew",
			mint,
		}},
		{"a crew member's own credential", AskSecret{Source: "crm", Crew: "yardmaster"}, []string{
			"lands in: crew member yardmaster's env_file, or crew/yardmaster.env in the yard's home when it has none",
			"read by: yardmaster's thread commands (yardr reply, thread open, thread state)",
			"not reached: declared calls (yardr call crm ...), which read the source's own file; other crew members' files; a session already running",
			mint,
		}},
		{"a user's own credential", AskSecret{Source: "crm", As: "ada"}, []string{
			"lands in: ada's env_file, or users/ada.env in the yard's home when it has none",
			"read by: calls made as ada (yardr call crm ... --as ada) and yardr source check crm --as ada, from the file at each call",
			"not reached: calls made without --as, thread commands, other users' files",
			mint,
		}},
		{"a user's own address", AskSecret{Source: "pushover", User: "ada"}, []string{
			"lands in: ada's env_file, or users/ada.env in the yard's home when it has none",
			"read by: the yard, at each message it sends ada through the source pushover",
			"not reached: calls and thread commands: it is an address, not a credential",
		}},
		{"a hook's signing secret", AskSecret{Source: "crm", Hook: "crm-events"}, []string{
			"lands in: hooks/crm-events.secret in the yard's home, mode 0600",
			"read by: the yard, to check each delivery to POST /hooks/crm-events",
			"not reached: any env_file, call or thread command",
		}},
		{"a waiting login's input", AskSecret{Login: "claude-a1b2", Env: []string{"INPUT"}}, nil},
		{"a harness key", AskSecret{Harness: "claude", Crew: "yardmaster", Env: []string{"ANTHROPIC_API_KEY"}}, nil},
	} {
		got := c.s.Reach()
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		// What a client is sent of the ask carries the reach: present on a
		// secret ask, absent on a harness key and a waiting login's input.
		if asked := (Ask{Secret: &c.s}).asked(); strings.Join(asked.Reach, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s as a client shows it: %q, want %q", c.name, asked.Reach, c.want)
		}
	}
}

// wantReachInCard says the card lines hold the reach of s, in the place the
// card puts it: right after the line that says where the value goes, and
// before how it is answered.
func wantReachInCard(t *testing.T, lines string, s *AskSecret) {
	t.Helper()
	reach := s.Reach()
	if len(reach) == 0 {
		t.Fatalf("the secret has no reach to find in the card:\n%s", lines)
	}
	block := "  it goes to a file only the yard's server reads; no agent is shown it\n  " + strings.Join(reach, "\n  ") + "\n"
	if !strings.Contains(lines, block) {
		t.Errorf("the card lacks the reach, after the line that says where the value goes:\n%s\nwant:\n%s", lines, block)
	}
	if strings.Count(lines, "lands in:") != 1 {
		t.Errorf("the card says where the value lands %d times, want once:\n%s", strings.Count(lines, "lands in:"), lines)
	}
}
