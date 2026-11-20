package agents

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"yardr.dev/yardr/harnesses"
	"yardr.dev/yardr/internal/store"
)

// DefaultKind is what the herdr runner starts when a config names no kind.
const DefaultKind = "claude"

// modelLists say how each kind lists the models of the installed agent,
// which is code, not something a file can say (see models.go).
var modelLists = map[string]func(ctx context.Context) ([]string, error){
	"agy":      agyModels,
	"claude":   claudeModels,
	"codex":    codexModels,
	"grok":     grokModels,
	"kimi":     kimiModels,
	"kiro":     kiroModels,
	"muse":     museModels,
	"opencode": opencodeModels,
	"pi":       piModels,
}

// Harnesses are the kinds yardr has a file for, in display order (see
// order). They are read once, when the program starts: the files are part
// of the binary, so one that cannot be read is a fault of the build, which
// TestHarnessFiles finds.
var Harnesses = func() []store.Harness {
	hs, err := Load(harnesses.FS)
	if err != nil {
		panic("yardr's own harness files: " + err.Error())
	}
	return hs
}()

// NetworkErrors are provider-agnostic transport failure patterns shared across
// every harness kind, tried only on error lines (see IsErrorLine) without an
// explicit non-gateway HTTP status (see HasNonGatewayHTTPStatus) after each
// kind's own non-other lines (auth, rate-limit, capacity, harness-specific
// network) and before its generic other catch-all lines (see dispatch.HarnessError).
//
// Each entry's source:
//  1. POSIX / Node.js libuv / getaddrinfo errno codes.
//  2. Node.js _http_client / undici "socket hang up" message.
//  3. Go standard library net / net/http timeout messages.
//  4. POSIX strerror / Rust reqwest / Python httpx / observed pane phrases
//     pairing a transport noun with a failure verb.
//  5. POSIX EHOSTUNREACH strerror and HTTP/2/reqwest unexpected-close strings.
//  6. SDK/harness connection and network failure phrases (Anthropic JS SDK
//     APIConnectionError, Node fetch failed, Muse network-open failure).
//  7. HTTP 502/504 gateway status phrases ("502 Bad Gateway",
//     "504 Gateway Time-?out"), never bare 502/504 numbers or 503/529.
var NetworkErrors = func() []store.ErrorLine {
	lines := []store.ErrorLine{
		{Reason: store.ReasonNetwork, Pattern: `\b(ECONNRESET|ECONNREFUSED|ETIMEDOUT|ECONNABORTED|EPIPE|ENOTFOUND|EAI_AGAIN|ENETUNREACH|EHOSTUNREACH)\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\bsocket hang up\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\b(tls handshake timeout|i/o timeout)\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\b(connection|network|stream|socket)\s+((was|is)\s+)?(reset|refused|dropped|lost|timed out|unreachable|disconnected)\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\b(no route to host|connection closed (unexpectedly|before |by ))\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\b((connection|network) (request )?(error|failed|could not be opened)|request timed out|fetch failed)\b`},
		{Reason: store.ReasonNetwork, Pattern: `(?i)\b(502 Bad Gateway|504 Gateway Time-?out)\b`},
	}
	if err := store.CompileErrorLines(lines); err != nil {
		panic("yardr's shared network error lines: " + err.Error())
	}
	return lines
}()

// errorLead matches the start of a harness error line after its TUI marks are
// stripped, so shared network patterns never fire on ordinary agent prose.
var errorLead = regexp.MustCompile(`(?i)^(API Error\b|(Error|TypeError|FetchError|NetworkError):|stream error:|request failed\b|fetch failed\b|model failed:|exceeded retry limit\b|unexpected status\b|connection error($|[:.!(—-]|\s+(occurred|while|during|when|to|from|in|with)\b))`)

// IsErrorLine reports whether a pane line is an error line of kind: either
// raw starts with kind's documented error-only glyph (Codex's ■), or said
// (the line after stripping Marks.Error) starts with a known error lead.
func IsErrorLine(kind, raw, said string) bool {
	if kind == "codex" && strings.HasPrefix(raw, "■") {
		return true
	}
	return errorLead.MatchString(said)
}

var (
	// httpStatusKeyword matches an HTTP 4xx/5xx code introduced by "http" or "status".
	httpStatusKeyword = regexp.MustCompile(`(?i)\b(?:http(?:[\s_-]*status(?:[\s_-]*code)?|/\d+(?:\.\d+)?)?|status(?:[\s_-]*code)?)\s*[:=]?\s*([45]\d\d)\b`)
	// httpStatusPhrase matches a 4xx/5xx code paired with a standard or provider HTTP reason phrase.
	httpStatusPhrase = regexp.MustCompile(`(?i)\b([45]\d\d)\s+(?:Bad\s+Request|Unauthorized|Payment\s+Required|Forbidden|Not\s+Found|Method\s+Not\s+Allowed|Not\s+Acceptable|Proxy\s+Authentication|Request\s+Timeout|Conflict|Gone|Length\s+Required|Precondition|Payload\s+Too\s+Large|Content\s+Too\s+Large|Request\s+Entity|URI\s+Too\s+Long|Unsupported\s+Media|Range\s+Not|Expectation\s+Failed|Misdirected|Unprocessable|Locked|Failed\s+Dependency|Too\s+Early|Upgrade\s+Required|Too\s+Many\s+Requests|Request\s+Header|Unavailable\s+For\s+Legal|Internal\s+(?:Server\s+)?Error|Not\s+Implemented|Service\s+Unavailable|HTTP\s+Version|Variant\s+Also|Insufficient\s+Storage|Loop\s+Detected|Not\s+Extended|Network\s+Authentication|Overloaded)\b`)
	// httpStatusLead matches a 4xx/5xx code at the start of an error payload.
	httpStatusLead = regexp.MustCompile(`(?i)(?:^|(?:error|failed|attempts|status)\s*:\s*|\]\s+)([45]\d\d)(?:$|[:,\s({[\]])`)
)

var nonStatusUnits = map[string]bool{
	"ms": true, "s": true, "sec": true, "secs": true, "second": true, "seconds": true,
	"minute": true, "minutes": true, "hour": true, "hours": true,
	"byte": true, "bytes": true, "kb": true, "mb": true, "gb": true,
	"line": true, "lines": true, "record": true, "records": true,
	"item": true, "items": true, "file": true, "files": true,
	"attempt": true, "attempts": true, "retry": true, "retries": true,
	"token": true, "tokens": true,
}

// HasNonGatewayHTTPStatus reports whether an error line carries an explicit
// 4xx/5xx HTTP status other than 502 or 504, so lines like 503/529/429 keep
// their own status reason or `other` even when transport words follow.
func HasNonGatewayHTTPStatus(said string) bool {
	for _, re := range []*regexp.Regexp{httpStatusKeyword, httpStatusPhrase} {
		for _, m := range re.FindAllStringSubmatch(said, -1) {
			if m[1] != "502" && m[1] != "504" {
				return true
			}
		}
	}
	for _, loc := range httpStatusLead.FindAllStringSubmatchIndex(said, -1) {
		code := said[loc[2]:loc[3]]
		if code == "502" || code == "504" {
			continue
		}
		if rest := strings.TrimLeft(said[loc[3]:], " \t"); rest != "" {
			word := strings.ToLower(strings.Fields(rest)[0])
			word = strings.TrimRight(word, ".,;:!?)]}")
			if nonStatusUnits[word] {
				continue
			}
		}
		return true
	}
	return false
}

// byName is each of Harnesses by the names it is asked for by.
var byName = index(Harnesses)

// order is the kinds offered first, in display order: the first is what a
// yard is created on when nothing says otherwise. A kind with a file that
// is not named here follows them, by name.
var order = []string{"claude", "codex", "grok", "kimi", "opencode", "pi"}

// index is each of hs by its kind and by its aliases.
func index(hs []store.Harness) map[string]store.Harness {
	m := map[string]store.Harness{}
	for _, h := range hs {
		m[h.Kind] = h
		for _, a := range h.Aliases {
			m[a] = h
		}
	}
	return m
}

// Lookup returns the harness of kind, if yardr has a file for it; the zero
// Harness, which knows nothing, if not.
func Lookup(kind string) (store.Harness, bool) {
	h, ok := byName[kind]
	return h, ok
}

// Kind returns the kind a herdr config names, or the runner's default when
// it does not name one.
func Kind(cfg map[string]any) string {
	if kind, _ := cfg["kind"].(string); kind != "" {
		return kind
	}
	return DefaultKind
}

// Load reads every <kind>.yaml of fsys into a Harness, in display order.
func Load(fsys fs.FS) ([]store.Harness, error) {
	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	var out []store.Harness
	names := map[string]string{}
	for _, file := range files {
		body, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		h := store.Harness{Kind: strings.TrimSuffix(file, ".yaml")}
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&h); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if err := h.Check(); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, n := range append([]string{h.Kind}, h.Aliases...) {
			if other, taken := names[n]; taken {
				return nil, fmt.Errorf("%s: %s is already %s's name", file, n, other)
			}
			names[n] = h.Kind
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		place := func(kind string) int {
			if i := slices.Index(order, kind); i >= 0 {
				return i
			}
			return len(order)
		}
		if a, b := place(out[i].Kind), place(out[j].Kind); a != b {
			return a < b
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// Models lists the installed agent's models, bounded in time. A kind
// without a source, or a failing tool, yields an empty list and the error.
func Models(h store.Harness, ctx context.Context) ([]string, error) {
	models := modelLists[h.Kind]
	if models == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return models(ctx)
}

// UnattendedArgs are kind's unattended flags (Flags.Unattended); nil when
// it has none.
func UnattendedArgs(kind string) []string { return byName[kind].Flags.Unattended }

// Preset is kind's permission preset called label, found by PresetLabel;
// false when the kind offers none of that name. It is how a named preset
// (yardr init --permissions, a yard's permissions_preset) is held to the
// harness that will run with it.
func Preset(kind, label string) (store.Permission, bool) {
	h, ok := Lookup(kind)
	if !ok {
		return store.Permission{}, false
	}
	want := store.PresetLabel(label)
	for _, p := range h.Flags.Permissions {
		if store.PresetLabel(p.Label) == want {
			return p, true
		}
	}
	return store.Permission{}, false
}

// PresetLabels are kind's permission presets' labels as its file writes
// them, in the file's order; nil when it offers none.
func PresetLabels(kind string) []string {
	h, ok := Lookup(kind)
	if !ok {
		return nil
	}
	out := make([]string, len(h.Flags.Permissions))
	for i, p := range h.Flags.Permissions {
		out[i] = p.Label
	}
	return out
}

// TrustEnv are the KEY=VALUE entries the pane of kind starts with for its
// folder-trust dialog. When enabled is false, each key is set to an empty
// value so the harness cannot inherit a trust bypass from herdr or env_file.
func TrustEnv(kind string, enabled bool) []string {
	h, ok := Lookup(kind)
	if !ok || h.Trust == nil {
		return nil
	}
	if enabled {
		return h.Trust.Env
	}
	out := make([]string, 0, len(h.Trust.Env))
	for _, e := range h.Trust.Env {
		k, _, _ := strings.Cut(e, "=")
		out = append(out, k+"=")
	}
	return out
}

// MemoryOff are the environment entries and harness arguments that switch
// kind's own memory off in a session the yard starts, so the yard's memory
// is the only one the session has. A kind with no switch reads (nil, nil).
// The runner puts the entries after the session's env file wins, and omits
// Codex's disabling argument when the session's own args enable memories,
// so the operator turns the harness's own memory back on per session
// (CLAUDE_CODE_DISABLE_AUTO_MEMORY=0, --enable memories).
//
// Claude Code's switch is the environment (code.claude.com/docs/en/env-vars,
// 2.1.310): CLAUDE_CODE_DISABLE_AUTO_MEMORY=1, "When disabled, Claude does
// not create or load auto memory files"; 0 forces it on. With it set the
// system prompt carries no memory section either (asked live, yardr-2eka).
// Its --bare and --safe-mode also disable auto memory but skip much else.
// Codex keeps its own in ~/.codex/memories, behind the memories feature
// (stable, off unless enabled); `codex --help` (0.160.1) documents
// --disable memories for it, and names no environment switch (only
// CODEX_HOME; CODEX_DISABLE_PROJECT_DOC is project docs, not memory). No
// other kind is researched: whether it keeps a memory of its own, and what
// switches it off, is unknown here.
func MemoryOff(kind string) (env, args []string) {
	switch kind {
	case "claude":
		return []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"}, nil
	case "codex":
		return nil, []string{"--disable", "memories"}
	default:
		return nil, nil
	}
}

// MemoryDir is where kind's harness keeps its own memory for project under
// the user's home; false when no such place is known. Only names are ever
// read from it (see doctor); its contents are the harness's. Claude Code's
// project is the git repository root, shared across its worktrees and
// subdirectories; outside git it is the project root. Every character
// outside letters, digits and "-" reads as "-", as its project directories
// show (yardr-2eka). Codex's memories are global to ~/.codex, not per
// project, and off unless enabled, so no directory names them.
func MemoryDir(kind, home, project string) (string, bool) {
	if kind != "claude" {
		return "", false
	}
	var b strings.Builder
	for _, r := range project {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return filepath.Join(home, ".claude", "projects", b.String(), "memory"), true
}

// UnattendedHelp lists the unattended flags per kind, one line each, for
// command help.
func UnattendedHelp() string {
	var b strings.Builder
	for _, k := range Kinds() {
		if args := UnattendedArgs(k); args != nil {
			b.WriteString("  " + k + ": " + strings.Join(args, " ") + "\n")
		}
	}
	b.WriteString("  any other kind: none; it may stop to ask")
	return b.String()
}

// VersionArgs are the arguments that make kind's harness print its version
// and exit; nil when none is known.
func VersionArgs(kind string) []string { return byName[kind].Flags.Version }

// VersionContains is what the output of kind's version flag must contain for
// the program to be that harness (Flags.VersionContains); "" when the file
// names none, and any program that answers the flag is taken for it.
func VersionContains(kind string) string { return byName[kind].Flags.VersionContains }

// harnessVersionTimeout bounds reading an installed harness's version; a
// test shortens it. A harness answers in well under a second, but five
// seconds ran out under load even on a stand-in that answers at once, so
// this is the twenty Models allows: both ask one installed tool for one
// short answer.
var harnessVersionTimeout = 20 * time.Second

// HarnessVersion is the version the installed harness of kind reports: the
// first line its file's version flags print, "" when the file names none,
// the harness is not installed, or it does not answer in time. It runs a
// program but changes nothing, as doctor's probe does.
func HarnessVersion(ctx context.Context, kind string) string {
	h, ok := Lookup(kind)
	if !ok || len(h.Flags.Version) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, harnessVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, store.Executable(kind), h.Flags.Version...).Output()
	if err != nil {
		return ""
	}
	return versionLine(string(out))
}

// versionLine is the first line of out that is not blank, cut to a length
// an event carries.
func versionLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > 60 {
				l = string(r[:60]) + "…"
			}
			return l
		}
	}
	return ""
}

// OneShotArgs are the arguments that make kind's harness answer prompt and
// exit: one real call to its model. nil when no such form is known.
func OneShotArgs(kind, prompt string) []string {
	args := byName[kind].Flags.OneShot
	if len(args) == 0 {
		return nil
	}
	return append(append([]string{}, args...), prompt)
}

// InstallHint is where to get kind's harness: the command or page known for
// it, else a pointer to the agent's own documentation.
func InstallHint(kind string) string {
	h, ok := Lookup(kind)
	if h.Install != "" {
		return h.Install
	}
	name := kind
	if ok {
		name = h.Label
	}
	return "see " + name + "'s own documentation; it must be on PATH as " + store.Executable(kind)
}
