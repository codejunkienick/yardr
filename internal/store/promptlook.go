// Where a session's prompt is read from, and whether a card can carry a
// prompt whole: the one lookup a brief, the depot's source list and the
// Line read share, so they cannot disagree about which file a session
// would be given or what a card would show. It lives in the store because
// the Line read is built there and the store cannot import the runner
// (which imports the store); the runner's readPrompt delegates here.
package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Where a prompt is found (PromptSource.Kind).
const (
	PromptDepot = "depot" // a file of the depot
	PromptHome  = "home"  // a file under the yard's home, found by name
	PromptFile  = "file"  // an absolute path, read as it is
)

// PromptSource says where LookupPrompt finds a prompt.
type PromptSource struct {
	Kind string
	// Path is relative to the depot's root for a file of the depot, else
	// absolute.
	Path string
	// Commit is the base commit a depot's file was read at; empty for one
	// read from the depot's working tree or a dir depot's directory.
	Commit string
}

// LookupPrompt reads the prompt p of a group working a bead of depot, by
// the lookup a brief makes, and says where it found the text. tried names
// every place looked, in order, where the prompt was not found there; a
// caller that cannot use the text answers with them and fails nothing
// else. An error that is not fs.ErrNotExist is the read's own (a depot
// whose base cannot be read), not a missing prompt.
func LookupPrompt(ctx context.Context, p string, depot *Depot, home string) (text []byte, src PromptSource, tried []string, err error) {
	if filepath.IsAbs(p) {
		text, err := os.ReadFile(p)
		return text, PromptSource{Kind: PromptFile, Path: p}, []string{p}, err
	}
	name, isRole := promptRoleName(p)
	if !isRole {
		return findPlainPrompt(p, depot, home)
	}
	if depot != nil && depot.Path != "" {
		rel := filepath.Join(".yardr", "roles", name)
		text, where, sha, err := readDepotPrompt(ctx, depot, rel)
		if err == nil {
			return text, PromptSource{Kind: PromptDepot, Path: rel, Commit: sha}, tried, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			tried = append(tried, where)
			return nil, PromptSource{}, tried, err
		}
		tried = append(tried, where)
	}
	if home != "" {
		path := filepath.Join(home, "roles", name)
		text, err := os.ReadFile(path)
		if err == nil {
			return text, PromptSource{Kind: PromptHome, Path: path}, tried, err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			tried = append(tried, path)
			return nil, PromptSource{Kind: PromptHome, Path: path}, tried, err
		}
		tried = append(tried, path)
	}
	if len(tried) == 0 {
		text, err := os.ReadFile(p)
		return text, PromptSource{Kind: PromptFile, Path: p}, []string{p}, err
	}
	return nil, PromptSource{}, tried, fmt.Errorf("%s not found (looked in %s)", p, strings.Join(tried, ", "))
}

// promptRoleName is the file name of a role prompt: roles/<name>.md or
// <name>.md.
func promptRoleName(p string) (string, bool) {
	p = filepath.Clean(p)
	dir, file := filepath.Split(p)
	if filepath.Ext(file) != ".md" || (dir != "" && dir != "roles"+string(filepath.Separator)) {
		return "", false
	}
	return file, true
}

// findPlainPrompt reads a relative prompt that is not a role from the
// depot's working tree, else the home.
func findPlainPrompt(p string, depot *Depot, home string) ([]byte, PromptSource, []string, error) {
	var tried []string
	depotDir := ""
	if depot != nil {
		depotDir = depot.Path
	}
	for _, dir := range []string{depotDir, home} {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, p)
		text, err := os.ReadFile(path)
		src := PromptSource{Kind: PromptHome, Path: path}
		if dir != home {
			src = PromptSource{Kind: PromptDepot, Path: p}
		}
		if err == nil {
			return text, src, tried, err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			tried = append(tried, path)
			return nil, src, tried, err
		}
		tried = append(tried, path)
	}
	if len(tried) == 0 {
		text, err := os.ReadFile(p)
		return text, PromptSource{Kind: PromptFile, Path: p}, []string{p}, err
	}
	return nil, PromptSource{}, tried, fmt.Errorf("%s not found (looked in %s)", p, strings.Join(tried, ", "))
}

// promptGitTimeout bounds the git a prompt read of a depot's base starts,
// as depotfile.GitTimeout bounds the same read for a brief.
const promptGitTimeout = 30 * time.Second

// readDepotPrompt reads rel at the tip of the depot's base, from the
// directory as it is for a dir depot: depotfile.ReadDepotFile's rule,
// without the import (depotfile imports the store). A file the base does
// not have is fs.ErrNotExist, whatever the working tree holds: the working
// tree is read only when the depot has no base that resolves (a directory
// that is no git checkout) or is a dir depot. where names the place
// looked, for the not-found answer, and sha the base commit read, empty
// for the working tree.
func readDepotPrompt(ctx context.Context, depot *Depot, rel string) (text []byte, where, sha string, err error) {
	if depot.Base != "" && !depot.IsDir() && !strings.Contains(depot.Base, "\n") {
		ctx, cancel := context.WithTimeout(ctx, promptGitTimeout)
		defer cancel()
		gitPath := filepath.ToSlash(rel)
		where = depot.Base + ":" + gitPath
		raw, rerr := ResolveRef(ctx, depot.Path, depot.Base+"^{commit}")
		if rerr == nil {
			sha = strings.TrimSpace(string(raw))
			list, lerr := git(ctx, depot.Path, "ls-tree", sha, "--", gitPath)
			if lerr != nil {
				return nil, where, "", lerr
			}
			if len(strings.TrimSpace(string(list))) == 0 {
				return nil, where, "", fs.ErrNotExist
			}
			text, err := git(ctx, depot.Path, "cat-file", "blob", sha+":"+gitPath)
			if err != nil {
				return nil, where, "", err
			}
			return text, where, sha, nil
		}
		if ctx.Err() != nil {
			return nil, where, "", ctx.Err()
		}
		// No base to read from: not a checkout, or its base does not
		// resolve. The working tree is all there is, as depotfile says.
	}
	path := filepath.Join(depot.Path, rel)
	text, err = os.ReadFile(path)
	return text, path, "", err
}

// promptCarriage is whether a card can carry the prompt file at path
// whole, and why not where it cannot: beside the path limit, a text
// longer than a card shows, or text a card does not show as written,
// cannot be carried (promptBefore's rules, lifted off the write
// transaction: homeFile is filesystem only, so no bolt read is opened).
// rewritten says the file is one the yard wrote by digest and no longer
// holds the text its name says.
func promptCarriage(home, path string) (text, why string, rewritten bool) {
	if !strings.HasPrefix(path, "/") {
		return "", "it is a relative path, read against each depot", false
	}
	real, data, sum, err := homeFileAt(home, "", path, MaxRunCrewRole, true)
	digestName := real
	if yardPromptName.MatchString(path) {
		digestName = path
	}
	switch {
	case err != nil:
		return "", "it does not stand as a regular file under the yard's home", false
	case len(data) > MaxRunCrewRole:
		return "", fmt.Sprintf("it is longer than the %d bytes a card shows", MaxRunCrewRole), promptDigestRewritten(digestName, sum)
	case !utf8.Valid(data) || strings.IndexFunc(string(data), crewRoleHidden) >= 0 || strings.Contains(string(data), NoteFilesMark) || strings.Contains(string(data), WeightMark):
		return "", "it is not text a card shows as written", promptDigestRewritten(digestName, sum)
	}
	return string(data), "", promptDigestRewritten(digestName, sum)
}

// promptDigest is the digest a yard-written prompt file is named by, in
// hex, "" where the file at path is no file the yard wrote so.
func promptDigest(path string) string {
	m := yardPromptName.FindStringSubmatch(path)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// yardPromptDigest is the digest of a prompt file the yard writes: one
// named by its text under the yard's roles directory. A depot file or an
// arbitrary file can have the same name without being one the yard wrote.
func yardPromptDigest(home, path string) string {
	if home == "" || path == "" {
		return ""
	}
	rel, err := filepath.Rel(filepath.Join(home, "roles"), filepath.Clean(path))
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return promptDigest(path)
}

// homeFileAt is homeFile without the transaction: the regular file at
// path that stands under the yard's home (home) once its symlinks are
// resolved, as that path, and as much of it as limit bytes and one more.
// When digest is true and the name or the resolved name carries a prompt
// digest, it also returns the SHA-256 of what it read (homeFile's words).
func homeFileAt(home, subject, path string, limit int, digest bool) (real string, data, sum []byte, err error) {
	homeAbs, homeReal, err := homeDirsAt(home, subject)
	if err != nil {
		return "", nil, nil, err
	}
	under := func(p string) bool {
		return strings.HasPrefix(p, filepath.Clean(homeAbs)+string(filepath.Separator)) || strings.HasPrefix(p, filepath.Clean(homeReal)+string(filepath.Separator))
	}
	if !under(filepath.Clean(path)) {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s is outside the yard's home: a card carries a prompt that stands under it", subject, filepath.Clean(path))
	}
	real, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s: %s", subject, path, OneLine(err.Error()))
	}
	if !strings.HasPrefix(real, filepath.Clean(homeReal)+string(filepath.Separator)) {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s resolves to %s, outside the yard's home: a card carries a prompt that stands under it", subject, path, real)
	}
	// Opened without blocking and judged by the open file, so a file that
	// became a pipe since it was named cannot hold the write.
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s: %s", subject, real, OneLine(err.Error()))
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s is not a regular file", subject, real)
	}
	var reader io.Reader = f
	var hasher interface{ Sum([]byte) []byte }
	if digest && (yardPromptName.MatchString(path) || yardPromptName.MatchString(real)) {
		h := sha256.New()
		reader, hasher = io.TeeReader(f, h), h
	}
	data, err = io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return "", nil, nil, fmt.Errorf("%s: prompt file %s: %s", subject, real, OneLine(err.Error()))
	}
	if hasher != nil {
		// The hash is of the bounded read alone, even when the file runs
		// past it: every prompt the yard writes fits the bound, so a
		// digest-named file longer than that no longer holds the text its
		// name says, and its prefix hash says so.
		return real, data, hasher.Sum(nil), nil
	}
	return real, data, nil, nil
}

// homeDirsAt is homeDirs without the transaction: the yard's home as
// named and as its symlinks resolve it, in the words of a refusal that
// names subject.
func homeDirsAt(home, subject string) (named, real string, err error) {
	if named, err = filepath.Abs(home); err != nil {
		return "", "", fmt.Errorf("%s: resolve the yard's home: %w", subject, err)
	}
	if real, err = filepath.EvalSymlinks(named); err != nil {
		return "", "", fmt.Errorf("%s: resolve the yard's home: %w", subject, err)
	}
	return named, real, nil
}
