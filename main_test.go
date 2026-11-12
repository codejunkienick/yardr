package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// repoGoFile is one non-test Go file the repository is built from: its path
// from the root, and its lines.
type repoGoFile struct {
	path  string
	lines []string
}

// loadRepoGoFiles walks the repository's non-test Go files once for the
// TestNoGoCodeNames* tests: three walks cost 31 s together under -race, one
// walk matched thrice costs one. loadRepoScan below scans those lines once
// for all four name tests, as four scans cost most of their seconds again
// (yardr-5372). The walkers run beside the package's other slow tests
// (t.Parallel), so the walk happens once however they interleave.
// It skips nothing but .git; a test looks past what its own walk skipped.
var loadRepoGoFiles = sync.OnceValues(func() ([]repoGoFile, error) {
	var out []repoGoFile
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		path = filepath.ToSlash(path)
		if d.IsDir() {
			if path == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, repoGoFile{path: path, lines: strings.Split(string(body), "\n")})
		return nil
	})
	return out, err
})

// scanHit is one line a name test cares about: its number (from 1), its
// trimmed text for the failure message, and what the test's pattern found
// in it. all holds every match of the role pattern, which a line can carry
// twice; credential and isImport are the GitHub test's other two checks of
// the same line, so its one pass over the line answers all three.
type scanHit struct {
	line       int
	text       string
	found      string
	all        []string
	credential bool
	isImport   bool
}

// repoFileScan is one file's hits for the four name tests, by test: role,
// notify, github and source (the core's). The tests share the walk above
// but used to each scan every line again: four scans cost most of their
// seconds under -race, one scan matched four ways costs one (yardr-5372).
// A test applies its own exceptions to the hits, as it did to the lines.
type repoFileScan struct {
	path   string
	role   []scanHit
	notify []scanHit
	github []scanHit
	source []scanHit
}

var (
	rolePattern       = regexp.MustCompile(`(roles/)?\b(builder|reviewer|merger)\.md\b`)
	notifyPattern     = regexp.MustCompile(`(?i)telegram|pushover`)
	githubPattern     = regexp.MustCompile(`(?i)github`)
	credentialPattern = regexp.MustCompile(`\bGITHUB_(?:TOKEN|APP_ID|APP_PRIVATE_KEY_FILE)\b`)
	importPattern     = regexp.MustCompile(`^\s*(\S+ )?"github\.com/[^"]*"(,)?\s*$`)
	sourcePattern     = regexp.MustCompile(`"github"|"linear"|hooks\.(GitHub|Linear)\b`)
)

// containsFold says whether s holds sub under ASCII case folding, as the
// name patterns' (?i) does for the ASCII names they look for. It is the
// scan's prefilter: a byte loop with no allocation, where a regexp search
// of every line cost the name tests their seconds under -race.
func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		j := 0
		for j < len(sub) {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				break
			}
			j++
		}
		if j == len(sub) {
			return true
		}
	}
	return false
}

// loadRepoScan scans the shared walk's lines once for the name tests. A
// regexp search of every line for every test is what it replaces: each
// pattern first gets a plain string prefilter (a fold for the (?i) names,
// a substring for the rest), and the regexp runs only on a line that
// holds its words, which few do. The hits keep what the regexp would
// have found, so a test's verdicts are its own as before.
var loadRepoScan = sync.OnceValues(func() ([]repoFileScan, error) {
	files, err := loadRepoGoFiles()
	if err != nil {
		return nil, err
	}
	out := make([]repoFileScan, 0, len(files))
	for _, f := range files {
		scan := repoFileScan{path: f.path}
		for i, line := range f.lines {
			if strings.Contains(line, ".md") {
				if all := rolePattern.FindAllString(line, -1); all != nil {
					scan.role = append(scan.role, scanHit{line: i + 1, text: strings.TrimSpace(line), all: all})
				}
			}
			if containsFold(line, "telegram") || containsFold(line, "pushover") {
				scan.notify = append(scan.notify, scanHit{line: i + 1, text: strings.TrimSpace(line), found: notifyPattern.FindString(line)})
			}
			if strings.Contains(line, "GITHUB_") || containsFold(line, "github") {
				if credentialPattern.MatchString(line) || githubPattern.MatchString(line) {
					scan.github = append(scan.github, scanHit{line: i + 1, text: strings.TrimSpace(line),
						found: githubPattern.FindString(line), credential: credentialPattern.MatchString(line),
						isImport: importPattern.MatchString(line)})
				}
			}
			if strings.Contains(line, `"github"`) || strings.Contains(line, `"linear"`) || strings.Contains(line, "hooks.") {
				if m := sourcePattern.FindString(line); m != "" {
					scan.source = append(scan.source, scanHit{line: i + 1, text: strings.TrimSpace(line), found: m})
				}
			}
		}
		out = append(out, scan)
	}
	return out, nil
})

func mustRepoScan(t *testing.T) []repoFileScan {
	t.Helper()
	files, err := loadRepoScan()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The roles are data in a pack: no Go code yardr is built from, outside the
// pack reader (internal/pack) and the packs themselves (packs/), names the
// file of a pack's role, so none can read one, write one or tell one from
// another. What a group does is its prompt's to say, and which file that is
// the pack's.
//
// Two files of help text show a group's prompt= by example
// (roles/builder.md, the relative form a depot or a home with roles of its
// own is read by): there the name is allowed in that form and no other.
// Tests are not held to this: they say what the embedded packs install, and
// build depots with roles of their own.
func TestNoGoCodeNamesARole(t *testing.T) {
	t.Parallel()
	examples := []string{"internal/cli/groupform.go", "internal/cli/ops.go"}
	var files int
	for _, f := range mustRepoScan(t) {
		// The pack reader and the packs name roles' files: this test
		// looks past what its own walk skipped.
		if strings.HasPrefix(f.path, "internal/pack/") || strings.HasPrefix(f.path, "packs/") {
			continue
		}
		files++
		for _, hit := range f.role {
			for _, m := range hit.all {
				if strings.HasPrefix(m, "roles/") && slices.Contains(examples, f.path) {
					continue
				}
				t.Errorf("%s:%d names %s: a role's file is the pack's to name (internal/pack, packs/): %s", f.path, hit.line, m, hit.text)
			}
		}
	}
	// Not for want of looking: the walk started at the repository's root.
	if files < 100 {
		t.Fatalf("%d Go files read", files)
	}
}

// The core asks a source's adapter what it can do and never compares a
// source's name: the adapters live in internal/hooks (and the trial
// stand-in in internal/standin), and no code outside them names one
// ("github", "linear") or names an adapter's type (hooks.GitHub). The one
// permitted list is internal/source's BuiltIn, which a declared source's
// name must keep clear of; it goes when the sources leave the binary
// (YRD-29).
func TestCoreComparesNoSourceName(t *testing.T) {
	t.Parallel()
	var files int
	for _, f := range mustRepoScan(t) {
		// The adapters and the trial stand-in name sources: this test
		// looks past them, as its own walk skipped their directories.
		if !strings.HasPrefix(f.path, "internal/") || strings.HasPrefix(f.path, "internal/hooks/") || strings.HasPrefix(f.path, "internal/standin/") {
			continue
		}
		files++
		for _, hit := range f.source {
			if f.path == "internal/source/declaration.go" && strings.Contains(hit.text, "BuiltIn") {
				continue
			}
			t.Errorf("%s:%d names a source: ask its adapter instead (internal/hooks): %s", f.path, hit.line, hit.text)
		}
	}
	// Not for want of looking: the walk read the core.
	if files < 100 {
		t.Fatalf("%d Go files read", files)
	}
}

// A source's calls go where its settings say: the region base its base
// setting names, or the host its host setting fills the declaration's
// template with. Every call, check and pairing of a credential asks
// store.Source.Resolved for the declaration it is made against, so that none
// reads the declaration's own base while the operator has set a host: a call
// would go to one address with the key given for another. The declaration's
// WithBase is called by what makes the choice alone: internal/source, which
// defines it, Resolved, and the places a setup card chooses a base or a host
// that no source holds yet: the sentences of its card, what its presets
// grant, and the check of the account typed with it.
func TestCallsAreMadeAgainstWhatTheSourceResolves(t *testing.T) {
	t.Parallel()
	allowed := map[string]int{
		"internal/store/sourcehost.go":           1,
		"internal/store/runask_setup.go":         1,
		"internal/store/runask_setup_address.go": 1,
		"internal/store/runask_setup_approve.go": 1,
	}
	files, err := loadRepoGoFiles()
	if err != nil {
		t.Fatal(err)
	}
	var read int
	for _, f := range files {
		if !strings.HasPrefix(f.path, "internal/") || strings.HasPrefix(f.path, "internal/source/") {
			continue
		}
		read++
		var seen int
		for i, line := range f.lines {
			if !strings.Contains(line, ".WithBase(") {
				continue
			}
			if seen++; seen > allowed[f.path] {
				t.Errorf("%s:%d calls WithBase: ask store.Source.Resolved for the declaration a source's calls are made against, so that its host is in place: %s", f.path, i+1, strings.TrimSpace(line))
			}
		}
	}
	// Not for want of looking: the walk read the core.
	if read < 100 {
		t.Fatalf("%d Go files read", read)
	}
}

// A service that tells a user outside the yard is a pack: no Go code yardr
// is built from names one, so none can know one from another. The packs
// live in packs/, their fixtures in tests, and the telling in each pack's
// setup notes and docs/pushover.md. The one permitted line is the go:embed
// directive that carries the packs in the binary, which names their
// directories as data: a bare word (yardr pack apply telegram) resolves
// through it. The other is the upgrade_from refusal
// (internal/store/settings.go), which names the two packs that set a notify
// source, by design (yardr-6ja6 decision 10): a refusal that cannot point
// at them leaves the operator with no next step.
func TestNoGoCodeNamesANotifyService(t *testing.T) {
	t.Parallel()
	var files int
	for _, f := range mustRepoScan(t) {
		files++
		for _, hit := range f.notify {
			if f.path == "packs/packs.go" && strings.Contains(hit.text, "go:embed") {
				continue
			}
			if f.path == "internal/store/settings.go" && strings.Contains(hit.text, "pack apply") {
				continue
			}
			t.Errorf("%s:%d names %s: a service that tells a user is a pack's to name (packs/, tests' fixtures, docs): %s", f.path, hit.line, hit.found, hit.text)
		}
	}
	// Not for want of looking: the walk started at the repository's root.
	if files < 100 {
		t.Fatalf("%d Go files read", files)
	}
}

// quotedTextShape is one free text an agent gives yardr, found written as a
// double-quoted argument by its pattern. Each has two samples: the quoted
// form, which the pattern must hold both in a Go string (\") and in a raw one
// ("), and the - form, which it must not, so that a pattern that stopped
// matching would not leave its shape unheld without a failure. A pattern of
// several alternatives holds one quoted sample for each: the first is quoted,
// the others also.
type quotedTextShape struct {
	name, pattern, quoted, dash string
	also                        []string
	// docsLeft is the bead that rewrites the examples the docs still show of
	// this shape. Until it lands TestDocsShowNoQuotedText leaves the shape
	// out, and it fails once no doc shows the shape, so that the field goes
	// with the last example and the docs are held to the shape from then on.
	docsLeft string
	re       *regexp.Regexp
}

// globalFlags are the flags every command takes (the root command's
// persistent ones, internal/cli/root.go), which may stand between yardr and a
// command's name: a pattern that starts at yardr would otherwise pass over
// `yardr --home $T/a mail ...`. The set is closed, and a value is read in
// either of its forms, because a pattern that took any flag and its value
// could take the addressee for one. A new global flag is named here.
const globalFlags = `( --home(=\S+| \S+)| --actor(=\S+| \S+)| --json)*`

// quotedTextShapes are the free texts that nothing an agent reads shows in
// double quotes (TestNoGoCodeShowsAQuotedText, TestDocsShowNoQuotedText), each
// held to its samples with every global flag in front and to the prose that
// is no command. A walker gets its own copy, since it compiles the patterns
// into it and the walkers run in parallel.
//
// A pattern runs from the command's name over the few words before the text
// to the quote that opens it; no more are allowed, or the closing quote of a
// Go string later on the line would be taken for it. Mail, reply and remember
// are English words as well, so their pattern starts at `yardr`, or at the %s
// that stands for it in a text that is given the program's path, and allows
// the global flags after it. The other patterns start at the command's own
// words, so a flag before them changes nothing.
func quotedTextShapes(t *testing.T) []quotedTextShape {
	t.Helper()
	shapes := []quotedTextShape{
		{name: "a bead note", pattern: `bead note \S+ \\?"|bead (done|advance)\b.*--note \\?"`,
			quoted: `yardr bead note web-1 "..."`, dash: `yardr bead note <id> -`},
		{name: "a mail", pattern: `(yardr|%s)` + globalFlags + ` mail (\S+ ){1,2}\\?"`,
			quoted: `yardr mail <crew member>@<peer> "..."`, dash: `yardr mail <name> -`},
		{name: "a reply", pattern: `(yardr|%s)` + globalFlags + ` reply (\S+ ){1,3}\\?"`,
			quoted: `yardr reply 4211 "..."`, dash: `yardr reply <seq> -`},
		{name: "a handoff", pattern: `crew handoff \S+ \\?"`,
			quoted: `yardr crew handoff %s "..."`, dash: `yardr crew handoff <name> -`},
		{name: "a journal entry", pattern: `crew journal \S+ \\?"`,
			quoted: `yardr crew journal %[1]s "..."`, dash: `yardr crew journal <name> -`},
		{name: "a memory", pattern: `(yardr|%s)` + globalFlags + ` remember (\S+ ){0,3}\\?"`,
			quoted: `yardr remember "<title>" -b "<body>"`, dash: `yardr remember - -b -`},
		{name: "a thread's title", pattern: `thread open (\S+ ){1,9}\\?"`,
			quoted: `yardr thread open github --crew yardmaster --in acme/webapp --assign jo -b body "Retries"`,
			dash:   `yardr thread open <source> --crew <member> --in <place> --assign <user> -b body -`},
		// An ask's texts are the question after the bead (or --new, or a
		// take-back's reason after the ask) and the flags that carry the rest,
		// which a wrapped example shows on lines of their own. The docs show
		// all four shapes still (docsLeft, yardr-w4dj).
		{name: "an ask's texts", docsLeft: "yardr-w4dj",
			pattern: `bead ask (\S+ ){1,3}\\?"|--take-back (\S+ ){2}\\?"|--(context|option|why|run|run-test) \\?"`,
			quoted:  `yardr bead ask <id> "<question>" --to <person>`, dash: `yardr bead ask <id> - --to <person>`,
			also: []string{`yardr bead ask --new "<question>"`, `yardr bead ask --take-back <id> <ask> "<reason>"`, `%s --take-back <id> <ask> "<reason>"`,
				`    --context "<line>" \`, `    --option "<label>: <consequence>" \`, `    --why "<why>"`, `    --run "<command>"`, `    --run-test "<command>"`}},
		// A pick's words follow its number, and --words and --decline take the
		// others; a link's "yardr bead answer <bead> <ask> " + flag shows no
		// text, and is no quote.
		{name: "an answer's words", docsLeft: "yardr-w4dj",
			pattern: `bead answer \S+ \S+ (\d+|<n>|%[sd]) \\?"|--(words|decline) \\?"`,
			quoted:  `yardr bead answer <id> <ask> 2 "not before Friday"`, dash: `yardr bead answer <id> <ask> --words -`,
			also: []string{`yardr bead answer <id> <ask> --words "<text>"`, `yardr bead answer <id> <ask> --decline "<words>"`}},
		{name: "a timer's reason", docsLeft: "yardr-w4dj", pattern: `timer add (\S+ ){1,9}\\?"`,
			quoted: `yardr timer add --in 40m "<reason>"`, dash: `yardr timer add --in 40m -`},
		{name: "a bead's title or body", docsLeft: "yardr-w4dj", pattern: `bead create (\S+ ){0,9}\\?"`,
			quoted: `yardr bead create "<title>" --depot web -b "<goal>"`, dash: `yardr bead create - --depot web -b -`},
	}
	// The line yardr doctor prints once a yard's first bead has merged, which
	// docs/doctor.md quotes: English that names a command's word and quotes
	// something after it is no command, which is why the patterns of mail,
	// reply and remember start at yardr.
	const prose = `Tell us it landed: mail yardmaster@yardr.dev with "landed".`
	for i := range shapes {
		sh := &shapes[i]
		sh.re = regexp.MustCompile(sh.pattern)
		// The samples behind each name of the program and each spelling of
		// the global flags, as a doc's trial block sets the home first.
		for _, program := range []string{"yardr", "%s"} {
			for _, flags := range []string{"", " --home $T/a", " --actor ada", " --actor=ada", " --json", " --json --home=$T/a --actor ada"} {
				for _, form := range append([]string{sh.quoted}, sh.also...) {
					quoted := strings.Replace(form, "yardr", program+flags, 1)
					if !sh.re.MatchString(quoted) || !sh.re.MatchString(strings.ReplaceAll(quoted, `"`, `\"`)) {
						t.Errorf("the pattern for %s misses %q", sh.name, quoted)
					}
				}
				dash := strings.Replace(sh.dash, "yardr", program+flags, 1)
				if sh.re.MatchString(dash) {
					t.Errorf("the pattern for %s holds the - form %q", sh.name, dash)
				}
			}
		}
		if sh.re.MatchString(prose) {
			t.Errorf("the pattern for %s holds prose, which is no command: %q", sh.name, prose)
		}
	}
	return shapes
}

// A refusal, a brief or a help text that shows a bead note, a mail, a reply,
// a handoff, a journal entry, a memory, a thread's title, an ask, an answer's
// words, a timer's reason or a bead's title or body as a double-quoted
// argument teaches the shape whose backtick spans and $( ) the shell runs
// before yardr starts: a reviewer's backticked file name ran a pull request's
// own test script (yardr-vmbn). So no Go code yardr is built from shows
// `yardr bead note <id> "..."`, a move's `--note "..."` or the like for the
// other free text an agent writes (yardr-tsrv, yardr-wrb8, yardr-755t); it names the -
// form, which reads the words from stdin, and leaves how they get there to the
// role, since one sandbox that reads these refusals cannot make a heredoc's
// temporary file (yardr-g8ky). Tests are not held to this: they say what the
// refusals must not.
func TestNoGoCodeShowsAQuotedText(t *testing.T) {
	t.Parallel()
	shapes := quotedTextShapes(t)
	var patterns []string
	for _, sh := range shapes {
		patterns = append(patterns, sh.pattern)
	}
	anyShape := regexp.MustCompile(strings.Join(patterns, "|"))
	files, err := loadRepoGoFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		for n, line := range f.lines {
			// Every shape needs the quote, and most lines have none.
			if !strings.Contains(line, `"`) || !anyShape.MatchString(line) {
				continue
			}
			// What a person types at a terminal is held out: the pairing form
			// prints one fixed greeting for the operator to mail the other
			// yard, with no code span for a shell to run and no words of an
			// agent's.
			if f.path == "internal/cli/pairform.go" && strings.Contains(line, `mail yardmaster@%s \"hello from the other yard\"`) {
				continue
			}
			for _, sh := range shapes {
				if sh.re.MatchString(line) {
					t.Errorf("%s:%d shows %s as a double-quoted argument, which the shell expands: name the - form, which reads the words from stdin (%s with the words on stdin): %s", f.path, n+1, sh.name, sh.dash, strings.TrimSpace(line))
					break
				}
			}
		}
	}
	// Not for want of looking: the walk started at the repository's root.
	if len(files) < 100 {
		t.Fatalf("%d Go files read", len(files))
	}
}

// A doc teaches a command by example as a refusal does, and an agent reads
// it: one that shows a bead note, a mail, a reply, a memory, a handoff, a
// journal entry, a thread's title, an ask, an answer, a timer or a bead's title
// or body as a double-quoted argument teaches the
// same shape, whose backtick spans and $( ) the shell runs before yardr
// starts. So the docs and the packs' setup notes are held to the shapes the
// Go texts are, and show the - form, which reads the words from stdin
// (yardr-ctwf rewrote 33 examples, and nothing held the next one back). The
// files are the ones git lists under docs/ and packs/*/SETUP.md, less
// docs/design/, which is history and shows the forms of its day. Test
// fixtures (testdata/) and contrib/ are outside these globs, and a wider glob
// leaves them out: no agent learns a command from a fixture, and contrib/'s
// READMEs are for the people who run its tools.
//
// A shape the docs still show stays out until the bead that its docsLeft names
// has rewritten them.
func TestDocsShowNoQuotedText(t *testing.T) {
	t.Parallel()
	shapes := quotedTextShapes(t)
	listed, err := exec.Command("git", "ls-files", "-z", "--", "docs/*.md", "packs/*/SETUP.md").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files int
	left := map[string]int{}
	for path := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
		if strings.HasPrefix(path, "docs/design/") {
			continue
		}
		// A file the index lists and the work tree has lost is not at the
		// tip, and a link or a submodule has no text of its own.
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files++
		for n, line := range strings.Split(string(body), "\n") {
			// Every shape needs the quote, and most lines have none.
			if !strings.Contains(line, `"`) {
				continue
			}
			for _, sh := range shapes {
				if !sh.re.MatchString(line) {
					continue
				}
				if sh.docsLeft != "" {
					left[sh.name]++
					continue
				}
				t.Errorf("%s:%d shows %s as a double-quoted argument, which the shell expands: show the - form, which reads the words from stdin (%s): %s", path, n+1, sh.name, sh.dash, strings.TrimSpace(line))
				break
			}
		}
	}
	for _, sh := range shapes {
		if sh.docsLeft != "" && left[sh.name] == 0 {
			t.Errorf("no doc shows %s as a double-quoted argument any more: delete its docsLeft (%s), so that the docs are held to the shape", sh.name, sh.docsLeft)
		}
	}
	// Not for want of looking: git lists the docs and the setup notes.
	if files < 40 {
		t.Fatalf("%d docs read", files)
	}
}

// GitHub left the core for packs/github: no Go code yardr is built from
// names it, except what the move audited (bead yardr-2c8b): the pack's own
// name; the stand-in and the trial sources, which serve its API shape so
// the pack proves without the real service; the problem and schema URL
// namespaces, the fetched programs' release addresses and the address the
// packs repository serves its packs from (internal/pack/repository.go),
// which live under github.com; and help texts, examples and comments that
// use GitHub as the example source. Tests are not held to this: they prove
// the pack against the stand-in. A line importing a Go module
// (github.com/...) cannot go.
func TestNoGoCodeNamesGitHub(t *testing.T) {
	t.Parallel()
	kept := []string{
		"contrib/isolated-yard/sources/main.go",
		"internal/bdp/server.go",
		"internal/cli/crew.go",
		"internal/cli/hooks.go",
		"internal/cli/link.go",
		"internal/cli/needs.go",
		"internal/cli/ops.go",
		"internal/cli/pack.go",
		"internal/cli/reply.go",
		"internal/cli/sourcecmd.go",
		"internal/cli/subscriptions.go",
		"internal/cli/threadopen.go",
		"internal/cli/threadstate.go",
		"internal/hooks/open.go",
		"internal/pack/repository.go",
		"internal/standin/standin.go",
		"packs/packs.go",
	}
	// A Go import of a module: an optional name, the quoted path, and
	// nothing else (importPattern, in the shared scan). Anything longer
	// that names github.com (a URL built or held) is the code's to own,
	// and fails outside the kept files.
	var files int
	for _, f := range mustRepoScan(t) {
		files++
		for _, hit := range f.github {
			if hit.credential {
				t.Errorf("%s:%d names a GitHub credential: credentials belong to a source pack, not Go code: %s", f.path, hit.line, hit.text)
			}
			if slices.Contains(kept, f.path) {
				continue
			}
			if hit.isImport {
				continue
			}
			if hit.found != "" {
				t.Errorf("%s:%d names %s: GitHub is a pack's to name (packs/github): %s", f.path, hit.line, hit.found, hit.text)
			}
		}
	}
	// Not for want of looking: the walk started at the repository's root.
	if files < 100 {
		t.Fatalf("%d Go files read", files)
	}
}

// ownHostWords are the words of a machine's own host name that no tracked
// file may carry: each one's length and the SHA-256 of its lowercase form,
// in hex. They are held as digests because a test that carried the words
// would be a hit of its own. To keep another word out, add the output of
// printf %s <word> | shasum -a 256, and the word's length.
var ownHostWords = []struct {
	length int
	digest string
}{
	{13, "0199c024260de59899233f90fd5dac0b956ced2a20361dc7d5c322564f46fe5c"},
	{10, "4ac2a8aa58aad7519a388e95a67c7f1c1167d325262845e8061a88db9c4fcac3"},
}

// namesOwnHost says whether line holds one of ownHostWords: a run of letters
// and digits, in any case, so a host name, a .local name and a URL each show
// the word in them. Only a run of a word's length is hashed, so the scan of
// the whole tree costs a fraction of a second.
func namesOwnHost(line string) bool {
	alnum := func(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' }
	for i := 0; i < len(line); {
		if !alnum(line[i]) {
			i++
			continue
		}
		j := i
		for j < len(line) && alnum(line[j]) {
			j++
		}
		for _, w := range ownHostWords {
			if j-i != w.length {
				continue
			}
			sum := sha256.Sum256([]byte(strings.ToLower(line[i:j])))
			if hex.EncodeToString(sum[:]) == w.digest {
				return true
			}
		}
		i = j
	}
	return false
}

// AGENTS.md keeps a machine's own facts out of what a user reads, and a host
// name is one: a doc or a test written on one machine names a placeholder
// (yard.example.net, or a ts.net name with a placeholder for the tailnet), and
// a test of a host name's rules holds the placeholder to them as it would the
// machine's own name. This reads the files git tracks, as git grep does. A hit
// names its file and line and not the line, so a failure does not print the
// word it is about.
func TestNoFileNamesAMachineHost(t *testing.T) {
	t.Parallel()
	listed, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files int
	for path := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
		// A file the index lists and the work tree has lost is not at the
		// tip, and a link or a submodule has no text of its own.
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files++
		for n, line := range strings.Split(string(body), "\n") {
			if namesOwnHost(line) {
				t.Errorf("%s:%d names a machine's own host name: write a placeholder (yard.example.net) in its place", path, n+1)
			}
		}
	}
	// Not for want of looking: git lists the whole tree.
	if files < 500 {
		t.Fatalf("%d files read", files)
	}
}

// The README is what a reader meets first, and it is kept short: what yardr
// is, its principles, the install, the concepts, a quick start, the commands
// and where the rest is. What is cut from it lives in docs/, so the README is
// mostly links, and a renamed heading or a moved page would leave one
// pointing at nothing. This follows every link to a file of the repository,
// and to a heading where the link names one, and holds the README to its
// length.
func TestReadmeLinksResolve(t *testing.T) {
	// It only reads the README and the pages it links to.
	t.Parallel()
	body, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "\n"); n >= 150 {
		t.Errorf("README.md has %d lines, want under 150: what is added moves to a page of docs/", n)
	}
	links := regexp.MustCompile(`\]\(([^)\s]+)\)`).FindAllStringSubmatch(string(body), -1)
	var local int
	for _, m := range links {
		file, anchor, _ := strings.Cut(m[1], "#")
		if strings.Contains(file, "://") {
			continue
		}
		local++
		if file == "" {
			file = "README.md"
		}
		page, err := os.ReadFile(filepath.FromSlash(file))
		if err != nil {
			t.Errorf("README.md links to %s: %v", m[1], err)
			continue
		}
		if anchor != "" && !slices.Contains(anchors(string(page)), anchor) {
			t.Errorf("README.md links to %s: %s has no heading with that anchor", m[1], file)
		}
	}
	// Not for want of looking: the README names its pages.
	if local < 5 {
		t.Fatalf("%d links to files found in README.md", local)
	}
}

// GitHub links a repository's SECURITY.md (here .github/SECURITY.md) from its
// Security tab and from the page that starts a new issue, so it is where a
// stranger who found a vulnerability learns how to say so. It has to keep
// saying two things: where a report goes, which is the private advisory form
// and never an issue anyone can read, and what a report may be about. This
// holds it to the link that opens the form and to a heading each for what is
// in scope and what is not.
func TestSecurityPolicy(t *testing.T) {
	// It only reads .github/SECURITY.md.
	t.Parallel()
	body, err := os.ReadFile(".github/SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	policy := string(body)
	const route = "https://github.com/yardr-dev/yardr/security/advisories/new"
	if !strings.Contains(policy, route) {
		t.Errorf("SECURITY.md does not link %s, the repository's private vulnerability reporting", route)
	}
	have := anchors(policy)
	for _, heading := range []string{"in-scope", "out-of-scope"} {
		if !slices.Contains(have, heading) {
			t.Errorf("SECURITY.md has no heading with the anchor #%s", heading)
		}
	}
}

// The UI dependencies still request go-runewidth v0.0.27, whose init builds
// lookup tables even for commands that never draw. Keep the selected version
// at the lazy-table release so a dependency change cannot restore that cost.
func TestRunewidthStartupInitRemainsLazy(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("build info is unavailable")
	}
	for _, dep := range info.Deps {
		if dep.Path != "github.com/mattn/go-runewidth" {
			continue
		}
		if dep.Version != "v0.0.28" {
			t.Fatalf("go-runewidth %s is selected; review its startup initialization before changing the v0.0.28 pin", dep.Version)
		}
		return
	}
	// The binary no longer pays this package's initialization cost if the
	// package is removed from its dependency graph entirely.
}

// anchors is the anchor of each heading of a Markdown page, as GitHub makes
// it: lowercase, spaces to hyphens, everything but letters, digits, hyphens
// and underscores dropped. Headings inside a code block are not headings.
func anchors(page string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		title := strings.TrimLeft(line, "#")
		if fenced || title == line || !strings.HasPrefix(title, " ") {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(title)) {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			}
		}
		out = append(out, b.String())
	}
	return out
}
