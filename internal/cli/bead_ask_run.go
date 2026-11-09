// How a bead ask carries a --run command's bytes beside argv: whether the
// command is a depot test ask, whose command --run-test carries, the role
// file a crew add ask carries, the prompt file a group add, group set or
// crew set ask carries and the declaration file a source add ask carries.
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// askRunCarried is the run ask --run names, as it is sent: the command as
// typed, in one string, for the yard to split and parse, and what that
// command carries beside it, each read once here so the store holds the
// bytes the card shows. test and role are the values of --run-test and
// --run-role, and testSet and roleSet say whether they were given.
func askRunCarried(command, test string, testSet bool, role string, roleSet bool) (*store.AskRun, error) {
	run := &store.AskRun{Argv: []string{command}}
	// Source add carries its file: read once here, beside the ask, so the
	// store holds the bytes the card shows.
	carried, err := sourceAddCarried(command)
	if err != nil {
		return nil, err
	}
	run.Carried = carried
	// Depot test carries its command from --run-test, beside the ask: --run
	// quotes nothing, so it never goes there.
	if depotTestCommand(command) {
		if !testSet {
			return nil, fmt.Errorf("the run ask depot test <depot> carries its command beside the ask, in --run-test <command> (T: on stdin)")
		}
		run.Test = test
	}
	// Crew add carries its role text from --run-role, or from the file
	// prompt= names: read once here, beside the ask, so the store holds the
	// bytes the card shows and a file changed after the ask changes nothing.
	if crewAddCommand(command) {
		if run.Role, err = crewAddRole(command, role, roleSet); err != nil {
			return nil, err
		}
	}
	// Group add carries the text of the prompt file its --set prompt=
	// names, the same way: the store holds the approval to a file that
	// still holds those bytes, and the yard reads no file for the card.
	if groupAddCommand(command) {
		if run.Role, err = groupAddPrompt(command); err != nil {
			return nil, err
		}
	}
	// Group set and crew set carry the text of the prompt file their prompt=
	// names the same way, when they name one.
	if setPromptCommand(command) {
		if run.Role, err = promptFileText(command); err != nil {
			return nil, err
		}
	}
	return run, nil
}

// plainRunLine reports whether command has only the characters a run ask's
// line takes (the store's parseRow). A command the store will refuse is
// refused there, carried or not, so no file is read for it.
func plainRunLine(command string) bool {
	if strings.ContainsRune(command, ',') {
		return false
	}
	for _, r := range command {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" \t._/=-@:[]", r) {
			continue
		}
		return false
	}
	return true
}

// groupAddCommand reports whether command is the run ask group add <name>
// ...: those words, with or without the leading yardr. The store parses
// it; this only says the ask may carry a prompt file's text beside it.
func groupAddCommand(command string) bool {
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	return len(words) >= 3 && words[0] == "group" && words[1] == "add"
}

// groupAddPrompt reads what a run ask of group add ... carries: the text
// of the file its --set prompt=<file> names, once, so the store holds the
// bytes the card shows. It answers "" for any other command, and for a
// group add that names no prompt. A file that cannot be read is refused
// here, and nothing is stored.
func groupAddPrompt(command string) (string, error) {
	if !groupAddCommand(command) {
		return "", nil
	}
	return promptFileText(command)
}

// setPromptCommand reports whether command is the run ask group set <name>
// ... or crew set <name> ...: those words, with or without the leading
// yardr. The store parses it; this only says the ask may carry a prompt
// file's text beside it, when it names one.
func setPromptCommand(command string) bool {
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	return len(words) >= 3 && (words[0] == "group" || words[0] == "crew") && words[1] == "set"
}

// promptFileText is the text of the file the prompt= of a --run command
// names, once, so the store holds the bytes the card shows. It answers ""
// for a command that names no prompt, and for one the store will refuse
// before it reads any file. A file that cannot be read is refused here, and
// nothing is stored.
func promptFileText(command string) (string, error) {
	if !plainRunLine(command) {
		return "", nil
	}
	file := crewPromptFile(command)
	if file == "" {
		return "", nil
	}
	data, err := readCrewRoleFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// crewAddCommand reports whether command is the run ask crew add <name>
// ...: those words, with or without the leading yardr. The store parses
// it; this only says the ask carries a role file beside it.
func crewAddCommand(command string) bool {
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	return len(words) >= 3 && words[0] == "crew" && words[1] == "add"
}

// crewAddRole reads what a run ask of crew add ... carries: the role
// file --run-role names, or the file prompt= names in the command,
// once, so the store holds the bytes the card shows. It answers "" for
// any other command, and for a crew add that names neither, which the
// store then refuses for carrying no role text. A file that cannot be
// read is refused here, and nothing is stored.
func crewAddRole(command, runRole string, runRoleSet bool) (string, error) {
	if !plainRunLine(command) || !crewAddCommand(command) {
		return "", nil
	}
	file := runRole
	if !runRoleSet {
		file = crewPromptFile(command)
		if file == "" {
			return "", nil
		}
	}
	if file == "" {
		return "", fmt.Errorf("--run-role needs a role file: the file whose text the crew add ask carries")
	}
	data, err := readCrewRoleFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// crewPromptFile is the file prompt= names in a crew add command, as
// typed: a bare prompt=<file>, or the terminal's --set prompt=<file>.
// "" where the command names none.
func crewPromptFile(command string) string {
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	for i := 0; i < len(words); i++ {
		word := words[i]
		if word == "--set" && i+1 < len(words) {
			i++
			word = words[i]
		} else if v, ok := strings.CutPrefix(word, "--set="); ok {
			word = v
		}
		if v, ok := strings.CutPrefix(word, "prompt="); ok {
			return v
		}
	}
	return ""
}

func readCrewRoleFile(file string) ([]byte, error) {
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: the role is not a regular file", store.OneLine(file))
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(store.MaxRunCrewRole)+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	if len(data) <= store.MaxRunCrewRole && !utf8.Valid(data) {
		return nil, fmt.Errorf("the role in %s is not valid UTF-8 text: a card carries text only", store.OneLine(file))
	}
	return data, nil
}

// depotTestCommand reports whether command is the run ask depot test
// <depot>: exactly those three words, with or without the leading yardr. The
// store parses it; this only says the ask carries --run-test beside it,
// so no character gate goes here: a command the store refuses is refused
// there, carried or not.
func depotTestCommand(command string) bool {
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	return len(words) == 3 && words[0] == "depot" && words[1] == "test"
}

// sourceAddCarried reads what a run ask of source add <file> carries:
// the declaration file once, resolved as yardr source add resolves it
// (a name yardr carries a source under, where no file stands), and each
// relative program's bytes from beside it as CopyCommandFiles reads
// them, each with its SHA-256. It answers nil for any other command. A
// file that cannot be read or does not parse is refused here, in the
// parser's words, and nothing is stored.
func sourceAddCarried(command string) ([]store.AskSource, error) {
	// Match parseRow's character gate before touching a path: a command
	// the store will reject must not make the CLI read a file first.
	if strings.ContainsRune(command, ',') {
		return nil, nil
	}
	for _, r := range command {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" \t._/=-@:[]", r) {
			continue
		}
		return nil, nil
	}
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "yardr" {
		words = words[1:]
	}
	if len(words) != 3 || words[0] != "source" || words[1] != "add" {
		return nil, nil
	}
	file := words[2]
	text, known, err := sourceAskDeclaration(file)
	if err != nil {
		return nil, err
	}
	d, err := source.Parse(file, text)
	if err != nil {
		return nil, fmt.Errorf("%s", store.OneLine(err.Error()))
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	declDir := filepath.Dir(abs)
	total := len(text)
	var programs []string
	seen := map[string]bool{}
	for _, c := range d.Commands() {
		if c.Relative() && !seen[c.Argv[0]] {
			seen[c.Argv[0]] = true
			programs = append(programs, c.Argv[0])
		}
	}
	var files []store.AskFile
	for _, prog := range programs {
		data, err := sourceAskCommandFile(known, declDir, prog, d.Name)
		if err != nil {
			return nil, fmt.Errorf("source %s: a command's program %s is a file beside the declaration's file: %v", d.Name, store.OneLine(prog), err)
		}
		total += len(data)
		if total > store.MaxRunSourceTexts {
			return nil, fmt.Errorf("source %s: its declaration with command files exceeds %d bytes together: a card carries at most that, so add it with yardr source add %s in a terminal", d.Name, store.MaxRunSourceTexts, file)
		}
		sum := sha256.Sum256(data)
		files = append(files, store.AskFile{Program: prog, Text: string(data), SHA256: hex.EncodeToString(sum[:])})
	}
	return []store.AskSource{{Name: d.Name, Text: string(text), Files: files}}, nil
}

// sourceAskDeclaration reads the declaration a source add ask names the way
// yardr source add reads its argument: the file, or where no file stands,
// the source yardr carries under that name, whose command files stand in
// known. A file is read as an ask reads one (readSourceAskFile), since this
// runs in a pane on a path the crew member chose.
func sourceAskDeclaration(file string) (text []byte, known fs.FS, err error) {
	if text, known, ok := knownFor(file); ok {
		return text, known, nil
	}
	text, err = readSourceAskFile(file, store.MaxRunSourceDeclaration, "the declaration")
	return text, nil, err
}

// sourceAskCommandFile reads the file the program prog of the source name
// names: from the source yardr carries (known), else from beside the
// declaration's file.
func sourceAskCommandFile(known fs.FS, declDir, prog, name string) ([]byte, error) {
	if known != nil {
		return fs.ReadFile(known, prog)
	}
	return readSourceAskFile(filepath.Join(declDir, prog), store.MaxRunSourceCommandFile, "source "+name+" command file "+store.OneLine(prog))
}

func readSourceAskFile(file string, limit int, what string) ([]byte, error) {
	f, err := os.OpenFile(file, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %s is not a regular file", store.OneLine(file), what)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", store.OneLine(file), store.OneLine(err.Error()))
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes: a run ask carries at most that, so add it with yardr source add %s in a terminal", what, limit, store.OneLine(file))
	}
	return data, nil
}
