package store

import (
	"testing"
	"time"
)

// The line a card shows of the head says what the bead records of it and no
// more, in absolute time, and never the facts of another head.
func TestHeadLine(t *testing.T) {
	t.Parallel()
	const sha = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	at := time.Date(2026, 11, 10, 9, 41, 0, 0, time.FixedZone("elsewhere", 3600))
	bead := func(meta map[string]any) *Bead { return &Bead{ID: "web-ab12", Metadata: meta} }
	told := func(t HeadTold) map[string]any {
		return map[string]any{MetaPRHead: sha, MetaPRHeadTold: t}
	}
	for _, c := range []struct {
		name string
		bead *Bead
		want string
	}{
		{"a push with its size", bead(told(HeadTold{Sha: sha, At: at, By: "hal", Action: "synchronize", Size: &HeadSize{Additions: 120, Deletions: 30, Files: 4}})),
			"Head: 1a2b3c4, pushed by hal at 2026-11-10 08:41 UTC, +120 -30 in 4 files, as the delivery that told it said."},
		{"one file, nothing removed", bead(told(HeadTold{Sha: sha, At: at, By: "hal", Action: "synchronize", Size: &HeadSize{Additions: 1, Files: 1}})),
			"Head: 1a2b3c4, pushed by hal at 2026-11-10 08:41 UTC, +1 -0 in 1 file, as the delivery that told it said."},
		{"opened, no size told", bead(told(HeadTold{Sha: sha, At: at, By: "hal", Action: "opened"})),
			"Head: 1a2b3c4, opened by hal at 2026-11-10 08:41 UTC, as the delivery that told it said."},
		{"an action with no verb", bead(told(HeadTold{Sha: sha, At: at, By: "hal", Action: "edited"})),
			"Head: 1a2b3c4, told by hal at 2026-11-10 08:41 UTC, as the delivery that told it said."},
		{"no one named", bead(told(HeadTold{Sha: sha, At: at, Action: "synchronize"})),
			"Head: 1a2b3c4, pushed at 2026-11-10 08:41 UTC, as the delivery that told it said."},
		{"no time", bead(told(HeadTold{Sha: sha, By: "hal", Action: "synchronize"})),
			"Head: 1a2b3c4, pushed by hal, as the delivery that told it said."},
		{"the size alone", bead(told(HeadTold{Sha: sha, Size: &HeadSize{Additions: 3, Deletions: 2, Files: 2}})),
			"Head: 1a2b3c4, +3 -2 in 2 files, as the delivery that told it said."},
		{"a record that says nothing", bead(told(HeadTold{Sha: sha})),
			"Head: 1a2b3c4. The delivery that told it said nothing of who pushed it, when or how large it is."},
		{"a head and no facts", bead(map[string]any{MetaPRHead: sha}),
			"Head: 1a2b3c4. The delivery that told it said nothing of who pushed it, when or how large it is."},
		// The facts are of the head they name: a head written by hand under
		// the facts of another is shown without them.
		{"facts of another head", bead(map[string]any{MetaPRHead: sha, MetaPRHeadTold: HeadTold{Sha: "ffffffffffff", At: at, By: "hal", Action: "synchronize"}}),
			"Head: 1a2b3c4. The delivery that told it said nothing of who pushed it, when or how large it is."},
		// As the store gives them back: metadata read from the yard is JSON.
		{"facts read back as JSON", bead(map[string]any{MetaPRHead: sha, MetaPRHeadTold: map[string]any{
			"sha": sha, "at": "2026-11-10T08:41:00Z", "by": "hal", "action": "synchronize",
			"size": map[string]any{"additions": float64(7), "deletions": float64(2), "files": float64(3)}}}),
			"Head: 1a2b3c4, pushed by hal at 2026-11-10 08:41 UTC, +7 -2 in 3 files, as the delivery that told it said."},
	} {
		if got := headLine(c.bead); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	none := headLine(bead(nil))
	if want := "Head: none told. No delivery of this pull request has named its head, so this card has none to accept: "; len(none) < len(want) || none[:len(want)] != want {
		t.Errorf("no head: %q", none)
	}
}
