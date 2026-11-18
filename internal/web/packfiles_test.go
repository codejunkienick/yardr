package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/web"
)

// packFileWant is one file of the full text behind the summary card the
// tests ask for: what its page must show, in the order of the pages.
type packFileWant struct {
	kind, name, action string
	oldSHA, newSHA     string
	diff, text         string
	// heading and shown are what the page draws for the name and for the
	// text where the pack's writer put marks of direction or control
	// characters in them: letters, the name on one line. Empty where the
	// page draws the pack's own words.
	heading, shown string
}

// trojanName and trojanText are words a pack's writer can write to make a
// page draw them in another order: a mark that turns the direction of text
// around, an isolate, an escape, a line separator and a line break.
const (
	trojanName = "roles/07\u202e\u2066.md\x1b[31m\u2028\nx.md"
	trojanText = "evil\u202e\x1b[31m.txt\u2028\n"
)

// The source starter adds: a declaration and the one command file it names.
const (
	packFileDecl   = "name: fresh\nbase: https://fresh.example.com/v1\ncredential: { env: FRESH_TOKEN, header: \"Authorization: Bearer {credential}\" }\nthread:\n  reply: [fresh/thread, reply]\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n"
	packFileThread = "#!/bin/sh\n# thread\n"
)

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// starterReading is the server's reading of a depot pack of 40 files, too
// large for a whole card: its texts pass the card's 128 KiB, and its files
// pass the card's lines, so the last of them have none. One file is a
// change and one carries text that must be written as text. The pack adds
// the source fresh, so its full text holds a declaration and a command file
// after the 40 files.
func starterReading() (*store.PackReading, []packFileWant) {
	plan := &store.AskPlan{}
	var wants []packFileWant
	for i := range 40 {
		text := strings.Repeat(fmt.Sprintf("line %d of the file\n", i), 180)
		w := packFileWant{kind: "file", name: fmt.Sprintf("roles/%02d.md", i), action: "create", newSHA: hexSum(text), text: text}
		diff := "--- was\n+++ is\n@@ -0,0 +1,1 @@\n+role\n"
		switch i {
		case 3:
			w.action, w.oldSHA = "change", hexSum("the file that stands\n")
			diff = "--- was\n+++ is\n@@ -1,2 +1,2 @@\n-the file that stands\n+line 3 of the file\n same\n"
			w.diff = diff
		case 5:
			w.text = "<b>bold</b> & \"quoted\" <script>alert(1)</script>\n" + text
			w.newSHA = hexSum(w.text)
		case 7:
			w.name, w.heading = trojanName, "roles/07.md[31m x.md"
			w.text = trojanText + text
			w.newSHA, w.shown = hexSum(w.text), store.NoteLetters(w.text)
		}
		plan.Objects = append(plan.Objects, store.AskPlanObject{Kind: "file", Name: w.name, Action: w.action,
			FileOldSHA: w.oldSHA, FileNewSHA: w.newSHA, Text: w.text, Diff: diff})
		wants = append(wants, w)
	}
	plan.Objects = append(plan.Objects,
		store.AskPlanObject{Kind: "group", Name: "builders", Action: "create"},
		store.AskPlanObject{Kind: "route", Name: "stage new", Action: "create"})
	wants = append(wants,
		packFileWant{kind: "declaration", name: "fresh", text: packFileDecl},
		packFileWant{kind: "command file", name: "fresh/thread", newSHA: hexSum(packFileThread), text: packFileThread})
	return &store.PackReading{Name: "starter", Kind: "git", Depot: "home", Label: "embedded in yardr test1", Build: "test1",
		Digest: strings.Repeat("d", 64), Plan: plan,
		Sources: []store.AskSource{{Name: "fresh", Text: packFileDecl,
			Files: []store.AskFile{{Program: "fresh/thread", Text: packFileThread, SHA256: hexSum(packFileThread)}}}}}, wants
}

// tinyReading is a depot pack a whole card holds.
func tinyReading() *store.PackReading {
	text := "role\n"
	return &store.PackReading{Name: "tiny", Kind: "git", Depot: "home", Label: "embedded in yardr test1", Build: "test1",
		Digest: strings.Repeat("e", 64), Plan: &store.AskPlan{Objects: []store.AskPlanObject{
			{Kind: "file", Name: "roles/a.md", Action: "create", FileNewSHA: hexSum(text), Text: text, Diff: "--- was\n+++ is\n@@ -0,0 +1,1 @@\n+role\n"}}}}
}

// preBlocks are the text blocks a file page draws, as a browser reads them:
// the change where the file is a changed one, then the text.
func preBlocks(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?s)<pre class="text file">(.*?)</pre>`).FindAllStringSubmatch(body, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// The hosted web opens every file of a summary card on a page of the bead,
// headed by its hashes, with links to the pages on either side and back to
// the card. The operator the card is asked of reads the whole pack there, and
// no other viewer is told any of it. The pages render the stored ask:
// reading writes nothing, and the pages end with the ask.
func TestPackFilePages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	y := newApproveYard(t, true)
	starter, wants := starterReading()
	y.own.SetPackReader(func(_ context.Context, name, depot string, _ store.Mappings, _ store.API) (*store.PackReading, error) {
		switch name {
		case "starter":
			r := *starter
			r.Depot = depot
			return &r, nil
		case "tiny":
			r := *tinyReading()
			r.Depot = depot
			return &r, nil
		}
		return nil, errors.New("no pack " + name)
	})
	askPack := func(bead, to, pack string) uint64 {
		t.Helper()
		seq, err := y.st.AddAsk(ctx, bead, store.Ask{Question: "Install " + pack + "?", To: to, Context: []string{"the depot has no way of working"},
			Run: &store.AskRun{Argv: []string{"pack apply " + pack + " --depot home"}}}, "yardmaster")
		must(t, err)
		return seq
	}
	// The bead's own words carry the trojan name too: its body, set where
	// the bead is made so no update diff stands in the history, and a
	// plain note beside the ask note's JSON.
	body := "the plan names " + trojanName
	b, err := y.st.CreateBead(ctx, store.NewBead{Title: "install starter", Stage: "new", Depot: "home", Body: body}, "yardmaster")
	must(t, err)
	seq := askPack(b.ID, "jo", "starter")
	must(t, y.st.AddNote(ctx, b.ID, "note names "+trojanName, "yardmaster"))
	jo, sam := y.token("jo@example.net"), y.token("sam@example.net")
	base := "/beads/" + b.ID + "/asks/" + strconv.FormatUint(seq, 10) + "/files/"

	v, err := y.st.Bead(ctx, b.ID, "jo")
	must(t, err)
	var run *store.RunAsked
	for _, a := range v.Asks {
		if a.Ask == seq {
			run = a.Run
		}
	}
	if run == nil || run.Pack == nil || !run.Pack.Summary {
		t.Fatalf("the card is not a summary: %+v", run)
	}
	files := run.Pack.Files
	folded := 0
	for _, f := range files {
		if f.Line < 0 {
			folded++
		}
	}
	if len(files) != 40 || folded == 0 || folded == 40 {
		t.Fatalf("the card ties %d files to lines and folds %d: the fixture must fold some and not all", len(files), folded)
	}
	head, err := y.st.Head(ctx)
	must(t, err)

	// The card on the bead's page: each file's line is the link to its
	// page, the first page has a link of its own, and a file with no line
	// has none: the pages beside it lead there.
	card := y.surfaceBody("/beads/"+b.ID, jo)
	if got := strings.Count(card, `class="opens"`); got != len(files)-folded+1 {
		t.Errorf("the card has %d links, want a line each for %d files and the first page", got, len(files)-folded)
	}
	for i, f := range files {
		// A file's line names it as letters, as its page does.
		link := `<a class="opens" href="` + base + strconv.Itoa(i+1) + `">file ` + store.OneLine(f.Name) + `: `
		if has := strings.Contains(card, link); has != (f.Line >= 0) {
			t.Errorf("file %s (line %d): the card's link stands %v", f.Name, f.Line, has)
		}
	}
	if want := `<a class="opens" href="` + base + `1">Read the full text from the first file</a>`; !strings.Contains(card, want) {
		t.Errorf("the card lacks %s", want)
	}

	// Every file opens whole, headed by its hashes, and the pager leads on.
	for i, w := range wants {
		n := i + 1
		r := y.getNoFollow(base+strconv.Itoa(n), jo)
		if r.status != http.StatusOK {
			t.Fatalf("GET file %d: %d: %.200s", n, r.status, r.body)
		}
		body := r.body
		blocks := preBlocks(body)
		text := or(w.shown, w.text)
		wantBlocks := []string{text}
		if w.diff != "" {
			wantBlocks = []string{w.diff, text}
		}
		if len(blocks) != len(wantBlocks) {
			t.Fatalf("file %d (%s): %d text blocks, want %d", n, w.name, len(blocks), len(wantBlocks))
		}
		for j := range blocks {
			if blocks[j] != wantBlocks[j] {
				t.Errorf("file %d (%s): block %d is %q, want %q", n, w.name, j, blocks[j], wantBlocks[j])
			}
		}
		// A text is written as text: markup in it is shown, not run, and the
		// page carries no inline style or script, which its policy refuses.
		if strings.Contains(body, "<style") || strings.Contains(body, " style=") || regexp.MustCompile(`<script(?:\s+defer)?>`).MatchString(body) {
			t.Errorf("file %d (%s) has inline style or script", n, w.name)
		}
		if strings.Contains(w.text, "<script>") && (strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "&lt;script&gt;alert(1)")) {
			t.Errorf("file %d (%s): markup in the text is not written as text", n, w.name)
		}
		for _, want := range []string{
			"<h2>" + html.EscapeString(or(w.heading, w.name)) + "</h2>",
			fmt.Sprintf("file %d of %d", n, len(wants)),
			", digest sha256:" + strings.Repeat("d", 64) + "</dd>",
			`<a class="bead" href="/beads/` + b.ID + `">`,
			`<a href="/beads/` + b.ID + `">Back to the card</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("file %d (%s) lacks %s", n, w.name, want)
			}
		}
		if w.kind == "file" {
			old := or(w.oldSHA, "(none)")
			if !strings.Contains(body, "<dd>sha256 "+old+"</dd>") || !strings.Contains(body, "<dd>sha256 "+w.newSHA+"</dd>") {
				t.Errorf("file %d (%s) is not headed by its hashes: old %s, new %s", n, w.name, old, w.newSHA)
			}
			if !strings.Contains(body, "file to "+w.action) {
				t.Errorf("file %d (%s) does not say it is to %s", n, w.name, w.action)
			}
		}
		if w.kind == "command file" && !strings.Contains(body, "<dd>sha256 "+w.newSHA+"</dd>") {
			t.Errorf("the command file is not headed by its hash")
		}
		// The hashes head the text: both stand before the first block.
		if first := strings.Index(body, "<pre"); w.newSHA != "" && strings.Index(body, w.newSHA) > first {
			t.Errorf("file %d (%s): the hash stands after the text", n, w.name)
		}
		// The pager stands above the text and below it, and its ends are
		// controls that are off.
		for _, end := range []struct {
			label string
			at    int
			to    int
		}{{"Previous", 1, n - 1}, {"Next", len(wants), n + 1}} {
			if n == end.at {
				if strings.Contains(body, ">"+end.label+"</a>") || strings.Count(body, `<span aria-disabled="true">`+end.label+`</span>`) != 2 {
					t.Errorf("file %d: %s is not off at the end", n, end.label)
				}
				continue
			}
			link := `<a href="` + base + strconv.Itoa(end.to) + `">` + end.label + `</a>`
			if got := strings.Count(body, link); got != 2 {
				t.Errorf("file %d: %d of %s, want one above the text and one below", n, got, link)
			}
		}
	}
	// A file that is no file of the pack has no page, however it is spelt.
	for _, n := range []string{"0", strconv.Itoa(len(wants) + 1), "-1", "01", "+1", "x", "1.0", "999999999999999999999"} {
		if r := y.getNoFollow(base+n, jo); r.status != http.StatusNotFound || strings.Contains(r.body, "line 0 of the file") {
			t.Errorf("GET file %s: %d, want 404 with no text", n, r.status)
		}
	}

	// The JSON is the page's value: the same file, whole.
	var page web.PackFilePage
	r := jwtGet(t, y.ts, base+"4", web.DefaultJWTHeader, jo, "Accept", "application/json")
	if err := json.Unmarshal([]byte(r.body), &page); err != nil || r.status != http.StatusOK {
		t.Fatalf("JSON of file 4: %d %v: %.200s", r.status, err, r.body)
	}
	if w := wants[3]; page.Bead != b.ID || page.Ask != seq || page.N != 4 || page.Of != len(wants) || page.Name != w.name ||
		page.Action != "change" || page.OldSHA256 != w.oldSHA || page.NewSHA256 != w.newSHA || page.Diff != w.diff ||
		page.Text != w.text || page.Bytes != len(w.text) || page.Insertions != 1 || page.Deletions != 1 ||
		page.Digest != strings.Repeat("d", 64) || page.Prev != base+"3" || page.Next != base+"5" || page.Card != "/beads/"+b.ID {
		t.Errorf("JSON of file 4 = %+v", page)
	}

	// A pack's writer can put marks of direction and control characters in
	// a file's name, as in its text (yardr-f3x6). The heading is checked in
	// the loop above; the JSON, the page's value, carries the name as
	// letters on one line, and no such character stands anywhere on the
	// page, so nothing the name draws can be reordered.
	var trojan web.PackFilePage
	r = jwtGet(t, y.ts, base+"8", web.DefaultJWTHeader, jo, "Accept", "application/json")
	if err := json.Unmarshal([]byte(r.body), &trojan); err != nil || r.status != http.StatusOK ||
		trojan.Name != wants[7].heading || trojan.Text != wants[7].shown {
		t.Errorf("JSON of file 8 = %+v (%d, %v), want the name %q and the text as letters", trojan, r.status, err, wants[7].heading)
	}
	trojanPage := y.getNoFollow(base+"8", jo).body
	for _, mark := range []rune{'‮', '⁦', '\x1b', ' '} {
		if strings.ContainsRune(trojanPage, mark) {
			t.Errorf("the page of file 8 carries %U", mark)
		}
	}

	// The card's weight line quotes the name too (the first install makes
	// the files), and the card reads it as letters on one line, as the file's
	// heading does, on each surface the card stands on: the bead's page, and
	// the rows of Home and the line (yardr-0wp5). Nothing of the name stands
	// there as a mark of direction, and no line of the card is reordered by it.
	// The command block holds the argv alone, which no file name reaches.
	hasBidi := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Bidi_Control, r) })
	}
	weightLine := regexp.MustCompile(`<p class="weighty">(.*?)</p>`)
	for _, surface := range approveSurfaces {
		body := y.surfaceBody(surface.path(b.ID), jo)
		// The notes below the card are the bead's log, an ask's among
		// them, read as letters: a surface of its own, checked below.
		if at := strings.Index(body, `id="notes"`); at >= 0 {
			body = body[:at]
		}
		var code []string
		for _, m := range weightLine.FindAllStringSubmatch(body, -1) {
			if line := html.UnescapeString(m[1]); strings.HasPrefix(line, store.WeightMark+store.WeightCode+": ") {
				code = append(code, line)
			}
		}
		if len(code) == 0 {
			t.Errorf("%s: no code weight line, want the one the first install carries", surface.name)
		}
		for _, line := range code {
			if !strings.Contains(line, "file "+wants[7].heading) || hasBidi(line) {
				t.Errorf("%s: the weight line is %q, want the name as letters (%q) and no mark of direction", surface.name, line, wants[7].heading)
			}
		}
		for _, mark := range []rune{'\x1b', '\u2028'} {
			if strings.ContainsRune(body, mark) {
				t.Errorf("%s carries %U", surface.name, mark)
			}
		}
		if hasBidi(body) {
			t.Errorf("%s carries a mark of direction", surface.name)
		}
	}
	if blocks := preBlocks(card); len(blocks) != 1 || blocks[0] != run.Command || !strings.Contains(run.Command, "pack apply starter --depot home") || strings.Contains(run.Command, "roles/") {
		t.Errorf("the card's command block is %q, want the argv command alone: %q", blocks, run.Command)
	}

	// The notes log draws each note's words as letters, as the terminal
	// does, an ask note's JSON among them (yardr-we6y): the trojan name
	// stands lettered in the notes region, and no mark of direction or
	// control character stands there. The ask note's weight line reads as
	// its file page's heading does; a plain note's words keep their line
	// break. The history below still quotes each note raw (yardr-fj37),
	// so the region ends where the sessions begin.
	beadHTML := y.surfaceBody("/beads/"+b.ID, jo)
	notesAt, sessionsAt := strings.Index(beadHTML, `id="notes"`), strings.Index(beadHTML, `id="sessions"`)
	if notesAt < 0 || sessionsAt < 0 || sessionsAt < notesAt {
		t.Fatalf("the bead's page has no notes log with sessions after it")
	}
	notes := beadHTML[notesAt:sessionsAt]
	for _, want := range []string{"file " + wants[7].heading, "note names roles/07.md[31m\nx.md"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes log does not read the trojan name lettered: no %q", want)
		}
	}
	if hasBidi(notes) {
		t.Errorf("the notes log carries a mark of direction")
	}
	for _, mark := range []rune{'\x1b', '\u2028'} {
		if strings.ContainsRune(notes, mark) {
			t.Errorf("the notes log carries %U", mark)
		}
	}

	// The body section reads the bead's words as letters too: the same
	// name, lettered, with its line break, and no mark of direction.
	bodyAt := strings.Index(beadHTML, `<h3 class="label">body</h3>`)
	if bodyAt < 0 {
		t.Fatalf("the bead's page has no body section")
	}
	section := beadHTML[bodyAt : bodyAt+strings.Index(beadHTML[bodyAt:], `</section>`)]
	if want := "the plan names roles/07.md[31m\nx.md"; !strings.Contains(section, want) {
		t.Errorf("the body section does not read the trojan name lettered: no %q", want)
	}
	if hasBidi(section) {
		t.Errorf("the body section carries a mark of direction")
	}

	// Stored bytes stay the record: the notes and the body still hold
	// the trojan name as it was written.
	kept, err := y.st.Notes(ctx, b.ID)
	must(t, err)
	var askText, plainText string
	for _, n := range kept {
		switch {
		case n.Seq == seq:
			askText = n.Text
		case n.Kind == "note":
			plainText = n.Text
		}
	}
	if !strings.Contains(askText, "roles/07\u202e") {
		t.Errorf("the stored ask note no longer holds the name's marks")
	}
	if !strings.Contains(plainText, trojanName) {
		t.Errorf("the stored plain note no longer holds what was written")
	}
	if got, err := y.st.GetBead(ctx, b.ID); err != nil || got.Body != body {
		t.Errorf("the stored body is %q, want %q", got.Body, body)
	}

	// The page's JSON form is the stored card, which the approval weighs again
	// and compares: the weight line keeps the name's marks as the store wrote
	// it, and it is the store's own, line for line.
	var cardPage web.BeadPage
	r = jwtGet(t, y.ts, "/beads/"+b.ID, web.DefaultJWTHeader, jo, "Accept", "application/json")
	if err := json.Unmarshal([]byte(r.body), &cardPage); err != nil || r.status != http.StatusOK {
		t.Fatalf("JSON of the bead page: %d %v: %.200s", r.status, err, r.body)
	}
	var pageWeight []store.AskWeight
	for _, a := range cardPage.Asks {
		if a.Ask == seq {
			pageWeight = a.Weight
		}
	}
	stored, err := y.st.Bead(ctx, b.ID, "jo")
	must(t, err)
	for _, a := range stored.Asks {
		if a.Ask == seq && !slices.Equal(a.Weight, pageWeight) {
			t.Errorf("the page's JSON weighs the card as %+v, the store as %+v", pageWeight, a.Weight)
		}
	}
	if len(pageWeight) != 1 || pageWeight[0].Class != store.WeightCode || !strings.Contains(pageWeight[0].Line, "file roles/07\u202e\u2066.md[31m x.md") {
		t.Errorf("the page's JSON weight is %+v, want one code line that keeps the name's marks", pageWeight)
	}

	// Reading wrote nothing: no event, and no blob of the pack, of a file,
	// or of anything the pages showed.
	if now, err := y.st.Head(ctx); err != nil || now != head {
		t.Errorf("the pages moved the yard from event %d to %d (%v)", head, now, err)
	}
	for _, hash := range []string{wants[0].newSHA, wants[3].oldSHA, strings.Repeat("d", 64), hexSum(packFileDecl)} {
		if r := y.getNoFollow("/blobs/"+hash, jo); r.status != http.StatusNotFound {
			t.Errorf("GET /blobs/%s: %d, want the yard to hold no blob of the pack", hash, r.status)
		}
	}

	// Nobody else is told a file, or that there is one. Each refusal is the
	// words of an ask that is not there, in HTML and in JSON, and names
	// nothing of the card.
	refused := func(who, path, token string) {
		t.Helper()
		for _, accept := range []string{"text/html", "application/json"} {
			got := jwtGet(t, y.ts, path, web.DefaultJWTHeader, token, "Accept", accept)
			if got.status != http.StatusNotFound || !strings.Contains(got.body, "has no file for you to read") {
				t.Errorf("%s (%s): GET %s: %d, want the words of an ask that is not there: %.200s", who, accept, path, got.status, got.body)
			}
			for _, told := range []string{"line 0 of the file", "roles/00.md", "<pre", "starter", strings.Repeat("d", 64)} {
				if strings.Contains(got.body, told) {
					t.Errorf("%s (%s): the refusal tells %q", who, accept, told)
				}
			}
		}
	}
	refused("a member who is no operator", base+"1", sam)
	refused("an ask that is not there", "/beads/"+b.ID+"/asks/9999/files/1", jo)
	if body := y.surfaceBody("/beads/"+b.ID, sam); strings.Contains(body, `class="opens"`) || strings.Contains(body, "/files/") {
		t.Errorf("a member who is asked nothing is shown the files")
	}

	// A card asked of another operator is that operator's alone. And the
	// page opens only while the addressee is an operator: once sam is a
	// member again, their own card reads as nobody's.
	must(t, func() error {
		_, err := y.st.SetUser(ctx, "sam", map[string]string{store.UserRole: store.RoleOperator}, "jo")
		return err
	}())
	toSam := y.bead("install starter for sam")
	samSeq := askPack(toSam.ID, "sam", "starter")
	samBase := "/beads/" + toSam.ID + "/asks/" + strconv.FormatUint(samSeq, 10) + "/files/"
	refused("an operator the card is not asked of", samBase+"1", jo)
	if r := y.getNoFollow(samBase+"1", sam); r.status != http.StatusOK || len(preBlocks(r.body)) != 1 {
		t.Fatalf("GET file 1 as the operator the card is asked of: %d", r.status)
	}
	if body := y.surfaceBody("/beads/"+toSam.ID, sam); !strings.Contains(body, `href="`+samBase+`1"`) {
		t.Errorf("the operator the card is asked of is shown no link")
	}
	must(t, func() error {
		_, err := y.st.SetUser(ctx, "sam", map[string]string{store.UserRole: store.RoleMember}, "jo")
		return err
	}())
	refused("a user who is no operator now", samBase+"1", sam)
	if body := y.surfaceBody("/beads/"+toSam.ID, sam); strings.Contains(body, `class="opens"`) || strings.Contains(body, "/files/") {
		t.Errorf("the card of a user who is no operator links the files")
	}

	// A card a whole card holds has no pages, and no link.
	small := y.bead("install tiny")
	smallSeq := askPack(small.ID, "jo", "tiny")
	if body := y.surfaceBody("/beads/"+small.ID, jo); strings.Contains(body, `class="opens"`) || strings.Contains(body, "/files/") {
		t.Errorf("a whole card links files")
	}
	if r := y.getNoFollow("/beads/"+small.ID+"/asks/"+strconv.FormatUint(smallSeq, 10)+"/files/1", jo); r.status != http.StatusNotFound || strings.Contains(r.body, "<pre") {
		t.Errorf("GET a whole card's file: %d, want 404 with no text: %.200s", r.status, r.body)
	}
	if r := y.getNoFollow("/beads/nobead-0000/asks/1/files/1", jo); r.status != http.StatusNotFound {
		t.Errorf("GET a file of a bead that is not there: %d, want 404", r.status)
	}

	// The pages end with the ask: declined, none of them opens.
	must(t, y.st.DeclineAsk(ctx, b.ID, seq, "not now", "jo"))
	for _, n := range []string{"1", "20", "42"} {
		refused("the ask declined", base+n, jo)
	}
}

// or is the first of two that is not empty.
func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
