package web_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// sidebarSection is the shell's sidebar part of the page: the markup the
// sidebar's nav holds.
func sidebarSection(t *testing.T, body string) string {
	t.Helper()
	_, s, ok := strings.Cut(body, `<nav class="sidebar"`)
	if !ok {
		t.Fatal("the page has no sidebar")
	}
	s, _, ok = strings.Cut(s, `</nav>`)
	if !ok {
		t.Fatal("the sidebar never closes")
	}
	return s
}

// sidebarYard is the populated yard with an operator, ready for asks: the
// depot line with the code and bugs flows, the depot loose with none, and
// the held bead in backlog.
func sidebarYard(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.populate()
	must(t, f.st.SetSetting(context.Background(), store.OperatorSetting, "mckean", "me"))
	return f
}

// The sidebar's counts: Arrivals is the open beads past their flow's first
// stage, Decide the beads carrying an open ask, and a flow with an asked
// bead shows the dot. The held bead with no ask counts in Needs but not in
// Decide, as the rail's pill counts.
func TestSidebarCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("arrivals is open minus first stage", func(t *testing.T) {
		f := sidebarYard(t)
		s := sidebarSection(t, squash(f.get("/").body))
		// Past their first stage: three in new, six off loose's missing
		// flow. One and two sit in backlog, five in triage.
		if want := `<a class="side-row" href="/line" title="Arrivals — 2 in transit" aria-label="Arrivals — 2 in transit">`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if want := `<span class="side-label">Arrivals</span><span class="side-count">2</span>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		for _, want := range []string{
			`<a class="side-row" href="/depots/line" title="line — 1 in transit" aria-label="line — 1 in transit"><span class="side-label">line</span><span class="side-transit">1</span></a>`,
			`<a class="side-row" href="/depots/loose" title="loose — 1 in transit" aria-label="loose — 1 in transit"><span class="side-label">loose</span><span class="side-transit">1</span></a>`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("the sidebar lacks %s: %s", want, s)
			}
		}
		// No ask anywhere: no Decide pill, no dot.
		for _, not := range []string{"side-pill", "side-dot"} {
			if strings.Contains(s, not) {
				t.Errorf("the sidebar carries %q with no ask: %s", not, s)
			}
		}
	})

	t.Run("decide is the rail pill, not needs", func(t *testing.T) {
		f := sidebarYard(t)
		three := f.byTitle()["three"]
		_, err := f.st.AddAsk(ctx, three.ID, pickAsk(), "yardmaster")
		must(t, err)
		body := f.get("/").body
		s := sidebarSection(t, squash(body))
		// The asked bead stands beside the held one in Needs, but Decide
		// counts the ask alone, as the rail's pill does on the same data.
		if want := `<span class="side-pill" aria-hidden="true">1</span>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if want := `<span class="pill" aria-hidden="true">1</span>`; !strings.Contains(body, want) {
			t.Errorf("the rail lacks %s: the sidebar's Decide is not the rail pill's count", want)
		}
		if want := `<a class="needs-count" href="/">2 wait for you</a>`; !strings.Contains(squash(f.get("/beads").body), want) {
			t.Errorf("the header lacks %s: the held bead with no ask counts in Needs", want)
		}
		// The asked bead's flow shows the dot; the other flow shows
		// nothing.
		if want := `<a class="side-flow" href="/depots/line/code" title="code — waits for you" aria-label="code — waits for you"><span class="side-dot" aria-hidden="true"></span><span class="side-label">code</span></a>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if want := `<a class="side-flow" href="/depots/line/bugs" title="bugs" aria-label="bugs"><span class="side-label">bugs</span></a>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
	})

	t.Run("a depot-less bead counts in the total alone", func(t *testing.T) {
		f := sidebarYard(t)
		_, err := f.st.CreateBead(ctx, store.NewBead{Title: "adrift", Stage: "new"}, "me")
		must(t, err)
		s := sidebarSection(t, squash(f.get("/").body))
		if want := `<span class="side-label">Arrivals</span><span class="side-count">3</span>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if n := strings.Count(s, "side-transit"); n != 2 {
			t.Errorf("the sidebar counts %d depot rows, want line and loose alone: %s", n, s)
		}
		if strings.Contains(s, "adrift") {
			t.Errorf("the sidebar names the depot-less bead: %s", s)
		}
	})

	t.Run("quiet depots fold", func(t *testing.T) {
		f := sidebarYard(t)
		must(t, f.st.PutDepot(ctx, store.Depot{Name: "empty", Base: "origin/main"}, "me"))
		s := sidebarSection(t, squash(f.get("/").body))
		if !strings.Contains(s, "Quiet depots · 1") {
			t.Errorf("the sidebar folds no quiet depot: %s", s)
		}
		if want := `<a class="side-row" href="/depots/empty"><span class="side-label">empty</span></a>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if strings.Contains(s, `>empty</span><span class="side-transit"`) {
			t.Errorf("the quiet depot stands unfolded: %s", s)
		}
	})

	t.Run("quiet flows fold", func(t *testing.T) {
		f := sidebarYard(t)
		fl, err := store.ParseFlow("draft -> done\ndone terminal\n")
		must(t, err)
		fl.Name = "docs"
		_, err = f.st.PutFlow(ctx, *fl, "me")
		must(t, err)
		must(t, f.st.SetFlowPointer(ctx, "line", "doc", "docs", "me"))
		s := sidebarSection(t, squash(f.get("/").body))
		if !strings.Contains(s, "1 quiet flow") {
			t.Errorf("the sidebar folds no quiet flow: %s", s)
		}
		if want := `<a class="side-flow" href="/depots/line/docs"><span class="side-label">docs</span></a>`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
	})

	t.Run("crew is bare and every row links a route", func(t *testing.T) {
		f := sidebarYard(t)
		s := sidebarSection(t, squash(f.get("/").body))
		if want := `<a class="side-row" href="/talk" title="Crew" aria-label="Crew">`; !strings.Contains(s, want) {
			t.Errorf("the sidebar lacks %s: %s", want, s)
		}
		if want := `<span class="side-label">Crew</span></a>`; !strings.Contains(s, want) {
			t.Errorf("Crew carries a count: %s", s)
		}
		for _, want := range []string{
			`<a class="yard-row" href="/line"`,
			`<a class="side-row" href="/line" title="Arrivals`,
			`<a class="side-row" href="/" title="Decide`,
			`<a class="side-row" href="/depots/line" title="line`,
			`<a class="side-flow" href="/depots/line/code" title="code`,
			`<a class="side-row" href="/config" title="Settings"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("the sidebar lacks %s: %s", want, s)
			}
		}
		// No bead title stands in the sidebar.
		for _, title := range []string{"one", "two", "three", "five", "six"} {
			if strings.Contains(s, ">"+title+"<") {
				t.Errorf("the sidebar names the bead %q: %s", title, s)
			}
		}
	})
}

// The light look: the same tokens with the design system's light values
// for a visitor whose system asks for light, with dark staying the
// default.
func TestSidebarLightTheme(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile("assets/yard.css")
	must(t, err)
	text := string(css)
	if !strings.Contains(text, "@media (prefers-color-scheme: light) {") {
		t.Fatal("the stylesheet has no light theme")
	}
	_, light, _ := strings.Cut(text, "@media (prefers-color-scheme: light) {")
	for _, want := range []string{
		"color-scheme: light;",
		"--ground: #F7F7F6;",          // --surface-app
		"--panel: #FFFFFF;",           // --surface-raised
		"--hairline: #E2E2DF;",        // --border-subtle
		"--hairline-strong: #CFCFCB;", // --border-strong
		"--ink: #161718;",             // --text-primary
		"--dim: #5B5E64;",             // --text-secondary
		"--faint: #8E9196;",           // --text-disabled
		"--running: #1F9A60;",         // --status-running
		"--held: #B3382C;",            // --status-error
		"--link: #1A56C8;",            // --text-link
	} {
		if !strings.Contains(light, want) {
			t.Errorf("the light theme lacks %s", want)
		}
	}
	// Dark stays: the base tokens keep their values.
	dark, _, _ := strings.Cut(text, "@media (prefers-color-scheme: light) {")
	for _, want := range []string{
		"color-scheme: dark light;",
		"--ground: #161718;",
		"--panel: #1c1d1f;",
		"--hairline: #27282b;",
		"--hairline-strong: #333538;",
		"--ink: #e9e9ea;",
		"--dim: #abadb0;",
		"--faint: #808286;",
		"--running: #4cc38a;",
		"--held: #e5675a;",
		"--link: #7aa2f7;",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("the dark tokens lack %s", want)
		}
	}
}
