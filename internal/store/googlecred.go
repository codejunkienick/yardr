package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// A Google secret ask (AskSecret.Google) asks for the sign-in of one
// Google account as the output of `gws auth export --unmasked` on the
// machine where that account signed in with `gws auth login`: gws
// cannot lead a sign-in on a hosted yard (its browser waits on a
// loopback redirect of the yard's own machine), so the export is
// pasted through a card instead and kept as the account's credentials
// file, which google-mint reads through
// GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE. The yard's own account is the
// operator's to give; a person's own account is that person's alone,
// the operator included among everyone refused (its paste holds their
// refresh token). Its Action stands here, not in policy.go, which is
// at its line ceiling.

// ActGiveGoogleCredentials guards giving a Google account's pasted
// credentials: Object.Name is the account, Object.To the yard user
// whose own account it is ("" for the yard's own).
const ActGiveGoogleCredentials Action = "give a Google account's credentials"

func init() { coreRules[ActGiveGoogleCredentials] = packable(ruleGiveGoogleCredentials) }

// googleCredentialsEnv is the one name a Google export is given under.
const googleCredentialsEnv = "GOOGLE_CREDENTIALS"

// googleServices are the gws services a Google login is made with, as
// docs/google.md's login names them: a card proposes the whole set,
// since the login's scopes are the yard's whole Google access.
var googleServices = []string{"gmail", "drive", "docs", "slides", "calendar"}

// ruleGiveGoogleCredentials is who may give a Google account's
// credentials: the yard's own is the operator's alone, as a source's
// credential is; a person's own is that person alone, never an
// agent's pane and not the operator for them. No pane gives either,
// and no session still starting, as an approval one user alone gives
// is refused (mayApproveSole): agentPane says whose pane a call came
// from, a starting session's included, before the actor is read.
func ruleGiveGoogleCredentials(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
	if pane, err := agentPane(ctx, tx); err != nil {
		return err
	} else if pane != "" {
		return fmt.Errorf("%s is no agent's to make; this is %s", obj.does(act), pane)
	}
	if obj.To == "" {
		return operatorOnly(ctx, tx, actor, obj.does(act), obj.Ask)
	}
	if tx.Bucket(bCrew).Get([]byte(actor)) != nil {
		return fmt.Errorf("only %s may %s; %s is a crew member", obj.To, obj.does(act), actor)
	}
	resolved, err := paneAuthor(tx, callerOf(ctx), actor)
	if err != nil {
		return err
	}
	if resolved != obj.To {
		return fmt.Errorf("only %s may %s; this is %s", obj.To, obj.does(act), or(resolved, "nobody"))
	}
	return nil
}

// GoogleCredentialsFile is where the yard keeps the pasted credentials
// of the Google account account: the yard's own (as "") in the gws
// default config, a person's in that account's own gws directory
// beside it, each under the home of the user the yard's server runs
// as, where google-mint looks with the same home. An account that is
// no single name has no file: it names the directory itself.
func GoogleCredentialsFile(account, as string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", &OperationalError{Err: fmt.Errorf("the yard keeps no Google credentials file: no home to keep it under (%v)", err)}
	}
	if as == "" {
		return filepath.Join(home, ".config", "gws", "credentials.json"), nil
	}
	if account == "" || account == "." || account == ".." || strings.ContainsAny(account, `/\`) ||
		strings.ContainsFunc(account, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", fmt.Errorf("google account %q names no credentials file (~/.config/gws-accounts/<account>/credentials.json)", account)
	}
	return filepath.Join(home, ".config", "gws-accounts", account, "credentials.json"), nil
}

// googleCredentialsJSON checks a pasted Google export and answers the
// bytes to keep: bare line breaks are stripped first (a single-line
// field strips them, and bare breaks are never inside a JSON string,
// whose breaks are escaped, so stripping loses nothing), then the
// value must be the authorized_user JSON gws auth export --unmasked
// prints, with its client id, client secret and refresh token. No
// refusal names the value.
func googleCredentialsJSON(value string) ([]byte, error) {
	stripped := strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(value))
	var cred struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
		Type         string `json:"type"`
	}
	if err := json.Unmarshal([]byte(stripped), &cred); err != nil || cred.Type != "authorized_user" ||
		cred.ClientID == "" || cred.ClientSecret == "" || cred.RefreshToken == "" {
		return nil, fmt.Errorf("the pasted value is no gws export: paste the whole output of gws auth export --unmasked, run on the machine that signed in with gws auth login (JSON with type authorized_user, client_id, client_secret and refresh_token); nothing was written")
	}
	return []byte(stripped), nil
}

// writeGoogleCredentials writes account's pasted credentials to its
// file (GoogleCredentialsFile), mode 0600, as writeHookSecret writes a
// hook's: whole to a second file beside it, synced and renamed into
// place. A file that is there is held to the rule it is read under
// and refused, not repaired, when it breaks it; so is a symbolic
// link. renamed says the rename was done. Errors name the account
// and the file, never the value. Each is a failure of the yard's own
// file, marked (OperationalError) as writeEnvFile marks its own, so
// a device that is no operator's is told that the yard could not do
// it and never the file's path (rpc's shapeUserErr).
func writeGoogleCredentials(account, as string, data []byte) (file string, renamed bool, err error) {
	file, err = GoogleCredentialsFile(account, as)
	if err != nil {
		return "", false, err
	}
	fail := func(err error) error { return &OperationalError{Err: err} }
	if st, err := os.Lstat(file); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return file, false, fail(fmt.Errorf("google account %s: the credentials file %s is a symbolic link, and the yard writes through none", account, file))
		}
		if !st.Mode().IsRegular() {
			return file, false, fail(fmt.Errorf("google account %s: the credentials file %s is not a regular file", account, file))
		}
		if perm := st.Mode().Perm(); perm&0o077 != 0 {
			return file, false, fail(fmt.Errorf("google account %s: the credentials file %s is open to others (mode %04o); chmod 600 it", account, file, perm))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return file, false, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return file, false, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	tmp := filepath.Join(filepath.Dir(file), "."+filepath.Base(file)+".new")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return file, false, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return file, false, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	err = func() error {
		defer f.Close()
		if err := f.Chmod(0o600); err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
		return f.Sync()
	}()
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		os.Remove(tmp)
		return file, false, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	dir, err := os.Open(filepath.Dir(file))
	if err == nil {
		err = dir.Sync()
		dir.Close()
	}
	if err != nil {
		return file, true, fail(fmt.Errorf("google account %s: %w", account, err))
	}
	return file, true, nil
}

// giveGoogle is giveSecret for a Google account's credentials: the
// pasted export is checked, the answer note is put, and last the file
// is written, in the order keySource keeps, so the store never says
// "gave" of a value the file does not hold.
func (w *wtx) giveGoogle(row *beadRow, ask uint64, sec *AskSecret, values map[string]string, actor string, k *sourceKey) error {
	if sec.As != "" {
		if _, err := getUser(w.Tx, sec.As); err != nil {
			return fmt.Errorf("a Google account's credentials are given for a user of the yard (yardr user list): %w", err)
		}
	}
	data, err := googleCredentialsJSON(values[sec.Env[0]])
	if err != nil {
		return err
	}
	self := source.Holder{Yard: sec.As == "", User: sec.As}
	if err := w.checkGoogleAccountHolder(sec.Google, self); err != nil {
		return err
	}
	text, err := json.Marshal(Answer{Ask: ask, Given: sec.Env})
	if err != nil {
		return err
	}
	if _, err := w.addGiven(row, string(text), actor); err != nil {
		return err
	}
	k.google, k.googleUser, k.names = sec.Google, sec.As, append([]string(nil), sec.Env...)
	file, renamed, err := writeGoogleCredentials(sec.Google, sec.As, data)
	k.file, k.renamed = file, k.renamed || renamed
	if err != nil {
		return err
	}
	if afterKeyRename != nil {
		return afterKeyRename()
	}
	return nil
}

// checkGoogleAccountHolder refuses a pasted sign-in for an account that
// another holder holds as a bound input of any source (bound.go): the
// per-account file is that holder's, and a paste by anyone else would
// replace their sign-in. A paste for an address nobody holds, or one
// the giver holds, stands. A source whose declaration does not read
// mints nothing, so it holds nothing here. It names the account, which
// the ask shows already, and the holder, as a bound giving's refusal
// does, and never the paste.
func (w *wtx) checkGoogleAccountHolder(account string, self source.Holder) error {
	return each(w.Tx, bSources, "", func(_ []byte, src *Source) error {
		d, err := src.Declaration()
		if err != nil || d.Credential == nil {
			return nil
		}
		for _, input := range d.Credential.Bound {
			held, err := w.boundHolder(src, input, account, self)
			if err != nil {
				return err
			}
			if held.IsZero() {
				continue
			}
			holder := "the yard"
			if !held.Yard {
				holder = held.User
			}
			return fmt.Errorf("google account %s is held by %s already (%s of source %s): a bound account belongs to one holder, so its sign-in is given by that holder alone", account, holder, input, src.Name)
		}
		return nil
	})
}
