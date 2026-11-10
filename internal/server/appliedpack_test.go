package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// A source pack as the operator keeps one in a repository: a declaration
// whose credential is minted by the command beside it, and a preset.
const (
	dirPackYAML = `name: dirmail
kind: source
sources:
  dirmail:
    declaration: dirmail.yaml
presets:
  read:
    about: Read mail
    grants:
      dirmail: [read]
`
	dirPackDecl = `name: dirmail
base: https://dirmail.example.com/v1
credential:
  env: DIRMAIL_TOKEN
  header: "Authorization: Bearer {credential}"
  mint: [DIRMAIL_ACCOUNT]
  mint_command: [dirmail-mint]
calls:
  search: { kind: read, request: { method: GET, path: /messages } }
`
	dirPackMint = "#!/bin/sh\n# mint a token from the account\n"
)

// dirGit runs git in dir as a committer of its own, whatever the machine's
// configuration says.
func dirGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return githubCarryGit(t, dir, append([]string{"-c", "user.name=ada", "-c", "user.email=ada@example.com", "-c", "commit.gpgsign=false"}, args...)...)
}

// dirPack writes the pack into a new directory, in no repository.
func dirPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"pack.yaml": dirPackYAML, "dirmail.yaml": dirPackDecl, "dirmail-mint": dirPackMint} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory that is in no repository stays in none, whatever the
	// directory the machine keeps its temporary files in is inside of. Git
	// compares the ceiling with the real path of where it stands.
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	return dir
}

// dirPackRepo is dirPack committed, so it is at a commit and clean.
func dirPackRepo(t *testing.T) string {
	t.Helper()
	dir := dirPack(t)
	dirGit(t, dir, "init", "-q")
	dirGit(t, dir, "add", "-A")
	dirGit(t, dir, "commit", "-q", "-m", "the pack")
	return dir
}

// appliedYard is a carry yard whose server reads the packs it embeds and
// the pack the operator applied from a directory, as the server does.
func appliedYard(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, home, bead := carryYard(t)
	st.SetAppliedPackReader(appliedReaderFor(home))
	return st, home, bead
}

// applyDir applies the pack in dir as the operator does in a terminal: the
// yard's record of it names the directory, its commit and whether it was
// clean.
func applyDir(t *testing.T, st *store.Store, home, dir string) {
	t.Helper()
	ctx := context.Background()
	p, err := pack.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pack.Build(ctx, st, p, pack.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: pack.Answers{}, Actor: "mckean"}); err != nil {
		t.Fatal(err)
	}
}

// setupOf is the setup card an ask stores.
func setupOf(t *testing.T, st *store.Store, bead string, seq uint64) *store.AskSetup {
	t.Helper()
	notes, err := st.Notes(context.Background(), bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range store.ResolveAsks(notes, time.Now()) {
		if r.Note.Seq == seq && r.Ask.Setup != nil {
			return r.Ask.Setup
		}
	}
	t.Fatalf("no setup ask %d on %s", seq, bead)
	return nil
}

// A pack the operator applied from a directory and left as it was gets the
// one setup card as an embedded pack does: the card carries the directory's
// sources and command files, labelled with the directory and the commit it
// was applied at, and approving it applies, takes the account and grants the
// picked preset, as the operator.
func TestSetupCardOfAnAppliedDirectoryIsCarriedOut(t *testing.T) {
	ctx := context.Background()
	st, home, bead := appliedYard(t)
	dir := dirPackRepo(t)
	applyDir(t, st, home, dir)
	commit := dirGit(t, dir, "rev-parse", "HEAD")

	to := store.Grantee{Crew: "yardmaster"}
	seq, err := st.AddAsk(ctx, bead, setupAskOf("dirmail", to), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	setup := setupOf(t, st, bead, seq)
	if setup.Label != dir+" at "+commit[:12] || setup.Kind != "source" || len(setup.Carried) != 1 {
		t.Fatalf("the card is of %q (kind %q), carrying %d sources", setup.Label, setup.Kind, len(setup.Carried))
	}
	carried := setup.Carried[0]
	if carried.Text != dirPackDecl || len(carried.Files) != 1 || carried.Files[0].Program != "dirmail-mint" || carried.Files[0].Text != dirPackMint {
		t.Fatalf("the card carries %+v", carried)
	}
	if _, err := st.ApproveSetup(ctx, bead, seq, 1, map[string]string{"DIRMAIL_ACCOUNT": "ops@example.com"}, "", "mckean", true); err != nil {
		t.Fatal(err)
	}
	drain(t, st, home, "boot-one")

	if rec := carryState(t, st, bead, seq); rec.State != store.CarryDone {
		t.Fatalf("the record = %+v", rec)
	}
	src, err := st.GetSource(ctx, "dirmail")
	if err != nil || src.Text != dirPackDecl {
		t.Fatalf("the stored source = %+v, %v", src, err)
	}
	copied, err := os.ReadFile(filepath.Join(home, "sources", "dirmail", "dirmail-mint"))
	if err != nil || string(copied) != dirPackMint {
		t.Fatalf("the copy of the mint = %q, %v", copied, err)
	}
	env, err := store.ReadEnvFile(filepath.Join(home, "sources", "dirmail.env"))
	if err != nil || source.Lookup(env.Env, "DIRMAIL_ACCOUNT") != "ops@example.com" {
		t.Fatalf("the account file = %+v, %v", env, err)
	}
	grantHeld(t, st, "dirmail", to, "read")
	// The carrying out applied the pack again, so the record names the
	// directory at the same commit: the next setup card is held to it too.
	recs, err := st.Packs(ctx)
	if err != nil || len(recs) != 1 || recs[0].Source != dir || recs[0].Commit != commit || recs[0].Dirty {
		t.Fatalf("the records after the setup = %+v, %v", recs, err)
	}
}

// A setup card of a pack yardr does not embed is refused, nothing stored,
// unless the operator applied it from a directory and left it as it was; each
// refusal says why and names the terminal road.
func TestSetupCardOfAnAppliedDirectoryRefusals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, st *store.Store, home string)
		want  string
	}{
		{"never applied", func(t *testing.T, st *store.Store, home string) {
			dirPackRepo(t)
		}, "has no record of applying one from a directory"},
		{"applied from a URL", func(t *testing.T, st *store.Store, home string) {
			rec := store.PackImport{Pack: "dirmail", Source: "https://example.com/packs/dirmail/"}
			if _, err := st.RecordPack(ctx, rec, true, "mckean"); err != nil {
				t.Fatal(err)
			}
		}, "applied it from the URL https://example.com/packs/dirmail/"},
		{"recorded as no directory", func(t *testing.T, st *store.Store, home string) {
			rec := store.PackImport{Pack: "dirmail", Source: "embedded in yardr 1.2.3"}
			if _, err := st.RecordPack(ctx, rec, true, "mckean"); err != nil {
				t.Fatal(err)
			}
		}, `names "embedded in yardr 1.2.3", which is no directory`},
		{"applied with uncommitted changes", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not committed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			applyDir(t, st, home, dir)
		}, "with uncommitted changes, so no commit says what was applied"},
		{"applied from no repository", func(t *testing.T, st *store.Store, home string) {
			applyDir(t, st, home, dirPack(t))
		}, "which was in no git repository then"},
		{"the directory moved to another commit", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			applyDir(t, st, home, dir)
			dirGit(t, dir, "commit", "-q", "--allow-empty", "-m", "later")
		}, "is at the commit"},
		{"the directory has changes now", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			applyDir(t, st, home, dir)
			if err := os.WriteFile(filepath.Join(dir, "dirmail-mint"), []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "has uncommitted changes now"},
		{"the repository is gone", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			applyDir(t, st, home, dir)
			if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
				t.Fatal(err)
			}
		}, "is in no git repository now"},
		{"the directory is gone", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			applyDir(t, st, home, dir)
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}, "does not hold it now"},
		{"the directory holds another pack", func(t *testing.T, st *store.Store, home string) {
			dir := dirPackRepo(t)
			applyDir(t, st, home, dir)
			other := strings.Replace(dirPackYAML, "name: dirmail", "name: another", 1)
			if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(other), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "holds the pack another now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, home, bead := appliedYard(t)
			tc.setup(t, st, home)
			_, err := st.AddAsk(ctx, bead, setupAskOf("dirmail", store.Grantee{Crew: "yardmaster"}), "yardmaster")
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "yardr pack apply") {
				t.Fatalf("the ask = %v, want a refusal with %q naming yardr pack apply", err, tc.want)
			}
			if notes, nerr := st.Notes(ctx, bead); nerr != nil || len(notes) != 0 {
				t.Fatalf("a refused ask stored %+v, %v", notes, nerr)
			}
		})
	}
}

// A pack applied from a directory that is not of kind source is refused in
// the words every such pack has, whatever commit its directory is at.
func TestSetupCardOfAnAppliedDirectoryRefusesAPackOfAnotherKind(t *testing.T) {
	ctx := context.Background()
	st, _, bead := appliedYard(t)
	dir := dirPack(t)
	if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte("name: dirmail\nkind: git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordPack(ctx, store.PackImport{Pack: "dirmail", Source: dir}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	_, err := st.AddAsk(ctx, bead, setupAskOf("dirmail", store.Grantee{Crew: "yardmaster"}), "yardmaster")
	if err == nil || !strings.Contains(err.Error(), "--setup sets up a pack of kind source, and pack dirmail is kind git") {
		t.Fatalf("the ask = %v", err)
	}
}

// A pack yardr embeds is read from the binary, whatever a record says of a
// directory by the same name: the embedded pack wins, as it does for every
// card.
func TestSetupCardPrefersTheEmbeddedPack(t *testing.T) {
	ctx := context.Background()
	st, _, bead := appliedYard(t)
	if _, err := st.RecordPack(ctx, store.PackImport{Pack: "google-mail", Source: t.TempDir()}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	seq, err := st.AddAsk(ctx, bead, setupAskOf("google-mail", store.Grantee{Crew: "yardmaster"}), "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if label := setupOf(t, st, bead, seq).Label; !strings.HasPrefix(label, "embedded in yardr ") {
		t.Fatalf("the card is of %q, not the embedded pack", label)
	}
}

// A directory that moves to another commit between the card and its
// approval, or between its approval and its carrying out, is not carried: the
// yard holds it to the commit the apply recorded at each step, and the
// carrying out applies nothing.
func TestSetupOfAnAppliedDirectoryIsHeldToItsCommit(t *testing.T) {
	ctx := context.Background()
	to := store.Grantee{Crew: "yardmaster"}

	t.Run("before the approval", func(t *testing.T) {
		st, home, bead := appliedYard(t)
		dir := dirPackRepo(t)
		applyDir(t, st, home, dir)
		seq, err := st.AddAsk(ctx, bead, setupAskOf("dirmail", to), "yardmaster")
		if err != nil {
			t.Fatal(err)
		}
		dirGit(t, dir, "commit", "-q", "--allow-empty", "-m", "later")
		_, err = st.ApproveSetup(ctx, bead, seq, 1, map[string]string{"DIRMAIL_ACCOUNT": "ops@example.com"}, "", "mckean", true)
		if err == nil || !strings.Contains(err.Error(), "is at the commit") {
			t.Fatalf("the approval = %v", err)
		}
	})

	t.Run("before the carrying out", func(t *testing.T) {
		st, home, bead := appliedYard(t)
		dir := dirPackRepo(t)
		applyDir(t, st, home, dir)
		seq := askSetupApprove(t, st, bead, "dirmail", 1, map[string]string{"DIRMAIL_ACCOUNT": "ops@example.com"}, to)
		before, err := st.GetSource(ctx, "dirmail")
		if err != nil {
			t.Fatal(err)
		}
		// The same commit, changed in the working tree: dirty.
		if err := os.WriteFile(filepath.Join(dir, "dirmail-mint"), []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		drain(t, st, home, "boot-one")

		rec := carryState(t, st, bead, seq)
		if rec.State != store.CarryFailed || !strings.Contains(rec.Result, "has uncommitted changes now") || !strings.Contains(rec.Result, "yardr pack apply") {
			t.Fatalf("the record = %+v", rec)
		}
		if _, err := os.Stat(filepath.Join(home, "sources", "dirmail.env")); !os.IsNotExist(err) {
			t.Fatalf("a refused carrying out wrote the account: %v", err)
		}
		if grants, err := st.SourceGrants(ctx, "dirmail"); err != nil || len(grants) != 0 {
			t.Fatalf("a refused carrying out granted: %+v, %v", grants, err)
		}
		if now, err := st.GetSource(ctx, "dirmail"); err != nil || now.Text != before.Text {
			t.Fatalf("a refused carrying out changed the source: %+v, %v", now, err)
		}
		if copied, err := os.ReadFile(filepath.Join(home, "sources", "dirmail", "dirmail-mint")); err != nil || string(copied) != dirPackMint {
			t.Fatalf("a refused carrying out changed the copy of the mint: %q, %v", copied, err)
		}
	})
}

// A setup card is of a pack the operator applied in a terminal: a record
// of another's apply is refused, since anyone of the crew may write one
// over the socket. A record of an operator's apply, by role or by the
// operator setting, is asked.
func TestSetupCardOfAnAppliedDirectoryHoldsTheRecordToTheOperator(t *testing.T) {
	ctx := context.Background()
	recordAt := func(t *testing.T, st *store.Store, dir, by string) {
		t.Helper()
		commit := dirGit(t, dir, "rev-parse", "HEAD")
		rec := store.PackImport{Pack: "dirmail", Source: dir, Commit: commit}
		if _, err := st.RecordPack(ctx, rec, true, by); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("another's apply is refused", func(t *testing.T) {
		st, _, bead := appliedYard(t)
		dir := dirPackRepo(t)
		recordAt(t, st, dir, "yardmaster")
		_, err := st.AddAsk(ctx, bead, setupAskOf("dirmail", store.Grantee{Crew: "yardmaster"}), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "says yardmaster applied it") || !strings.Contains(err.Error(), "yardr pack apply") {
			t.Fatalf("the ask = %v", err)
		}
		if notes, nerr := st.Notes(ctx, bead); nerr != nil || len(notes) != 0 {
			t.Fatalf("a refused ask stored %+v, %v", notes, nerr)
		}
	})

	t.Run("the operator's apply by role is asked", func(t *testing.T) {
		st, _, bead := appliedYard(t)
		if _, err := st.AddUser(ctx, "ada", "mckean"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetUser(ctx, "ada", map[string]string{store.UserRole: store.RoleOperator}, "mckean"); err != nil {
			t.Fatal(err)
		}
		dir := dirPackRepo(t)
		recordAt(t, st, dir, "ada")
		ask := setupAskOf("dirmail", store.Grantee{Crew: "yardmaster"})
		ask.To = "ada"
		seq, err := st.AddAsk(ctx, bead, ask, "yardmaster")
		if err != nil {
			t.Fatal(err)
		}
		commit := dirGit(t, dir, "rev-parse", "HEAD")
		if label := setupOf(t, st, bead, seq).Label; label != dir+" at "+commit[:12] {
			t.Fatalf("the card is of %q", label)
		}
	})
}
