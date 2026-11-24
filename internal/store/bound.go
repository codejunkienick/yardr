package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// A mint input whose name some source of the yard declares bound
// belongs to one holder across the whole yard: the yard, in any
// source's env file, or one user, in theirs. Giving refuses such a
// value another holder holds — for an input the target source itself
// binds, and for one it leaves unbound that another source binds —
// and a call made as a user refuses one its file names from another
// holder, so a file edited by hand is caught as a given one is. A
// name bound nowhere is never held, so a value every holder shares
// passes. The value itself is never kept or shown: only who holds
// it is answered.

// BoundHolder answers which holder other than except holds value of
// the bound input input of the source sourceName: the yard, a user, or
// zero where no other holder holds it. Pass a zero source.Holder{} for
// any holder, or the acting holder to check whether anyone else holds
// it. It reads every source's yard env file and every user's, as they
// stand, so a value the yard holds on another source refuses a paste.
// It is the helper a pasted sign-in asks before it writes (yardr-yt5k),
// beside the givings and calls here.
func (s *Store) BoundHolder(ctx context.Context, sourceName, input, value string, except source.Holder) (source.Holder, error) {
	var src *Source
	var sources []Source
	var users []User
	err := s.read(func(tx *bolt.Tx) error {
		src = new(Source)
		if ok, err := get(tx, bSources, sourceName, src); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("source %s: %w", sourceName, ErrNotFound)
		}
		if err := each(tx, bSources, "", func(_ []byte, other *Source) error {
			sources = append(sources, *other)
			return nil
		}); err != nil {
			return err
		}
		return each(tx, bUsers, "", func(_ []byte, u *User) error {
			users = append(users, *u)
			return nil
		})
	})
	if err != nil {
		return source.Holder{}, err
	}
	return BoundHolderIn(s.home, src, sources, users, input, value, except)
}

// boundHolder is BoundHolder inside a write, from its users and sources
// as they stand.
func (w *wtx) boundHolder(src *Source, input, value string, except source.Holder) (source.Holder, error) {
	var sources []Source
	if err := each(w.Tx, bSources, "", func(_ []byte, other *Source) error {
		sources = append(sources, *other)
		return nil
	}); err != nil {
		return source.Holder{}, err
	}
	var users []User
	if err := each(w.Tx, bUsers, "", func(_ []byte, u *User) error {
		users = append(users, *u)
		return nil
	}); err != nil {
		return source.Holder{}, err
	}
	return BoundHolderIn(w.home, src, sources, users, input, value, except)
}

// YardBoundNames is the set of mint input names some source of the
// yard declares bound, from sources as they stand: the yard-wide set a
// giving or a call is held to, so a name bound on one source is held
// on every source that mints with it, and one bound nowhere is held
// nowhere. It is worked out fresh at each check, with no cache, as
// the declarations stand then. A source whose declaration does not
// read fails the set: the holder of its bound names would be unseen,
// so the check that asked refuses (fail closed), where the pasted
// sign-in's check (googlecred.go) skips such a source, which mints
// nothing.
func YardBoundNames(sources []Source) (map[string]bool, error) {
	out := map[string]bool{}
	for i := range sources {
		d, err := sources[i].Declaration()
		if err != nil {
			return nil, err
		}
		if d.Credential == nil {
			continue
		}
		for _, name := range d.Credential.Bound {
			out[name] = true
		}
	}
	return out, nil
}

// yardBound is YardBoundNames for the yard as it stands inside w's
// write, from its sources in the same transaction.
func (w *wtx) yardBound() (map[string]bool, error) {
	var sources []Source
	if err := each(w.Tx, bSources, "", func(_ []byte, other *Source) error {
		sources = append(sources, *other)
		return nil
	}); err != nil {
		return nil, err
	}
	return YardBoundNames(sources)
}

// yardBound is YardBoundNames for the yard as it stands.
func (s *Store) yardBound() (map[string]bool, error) {
	var sources []Source
	err := s.read(func(tx *bolt.Tx) error {
		return each(tx, bSources, "", func(_ []byte, other *Source) error {
			sources = append(sources, *other)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return YardBoundNames(sources)
}

// boundLookup is a BoundHolder for the calls of src, with the yard's
// bound names beside it: which holder holds each bound value a call
// as a user names, read as the files stand. The names are the yard's
// as the call is made; an unreadable declaration refuses the call
// before anything mints.
func (s *Store) boundLookup(src *Source) (source.BoundHolder, map[string]bool, error) {
	yardBound, err := s.yardBound()
	if err != nil {
		return nil, nil, err
	}
	holder := func(input, value string, except source.Holder) (source.Holder, error) {
		var sources []Source
		var users []User
		err := s.read(func(tx *bolt.Tx) error {
			if err := each(tx, bSources, "", func(_ []byte, other *Source) error {
				sources = append(sources, *other)
				return nil
			}); err != nil {
				return err
			}
			return each(tx, bUsers, "", func(_ []byte, u *User) error {
				users = append(users, *u)
				return nil
			})
		})
		if err != nil {
			return source.Holder{}, err
		}
		return BoundHolderIn(s.home, src, sources, users, input, value, except)
	}
	return holder, yardBound, nil
}

// readUserBound is ReadUserCredentialBound for a call of src as a user,
// held to the yard's bound names as they stand: the credential in
// envFile for a call of kind, refused before anything mints where a
// mint input bound anywhere in the yard names another holder's value,
// and refused where a declaration of the yard does not read.
func (s *Store) readUserBound(ctx context.Context, d *source.Declaration, src *Source, user, envFile, kind string) (string, string, error) {
	holder, yardBound, err := s.boundLookup(src)
	if err != nil {
		return "", "", err
	}
	return d.ReadUserCredentialBound(ctx, user, envFile, src.mintIn(s.home), kind, ReadEnvFileEntries, holder, yardBound)
}

// BoundHolderIn is who holds value of input among the yard and users,
// skipping except (zero skips nobody). The yard is checked in every
// source's own file, unfiltered: src's file first, then the rest in
// sources order, so a value held twice reports the target first and a
// same-source hit reads as it always has. Skipping except matters when
// two holders share a value (by hand or before binding): stopping at
// self first would miss a second holder who sorts later. An except for
// the yard skips every yard file. An empty value is held by nobody. A
// file that is not there holds nothing; one that cannot be read fails,
// since it may hold the value unseen. Both the store and yardr source
// check --as call this one helper so their rules stay identical.
func BoundHolderIn(home string, src *Source, sources []Source, users []User, input, value string, except source.Holder) (source.Holder, error) {
	want := strings.TrimSpace(value)
	if want == "" {
		return source.Holder{}, nil
	}
	if !except.Yard {
		held, err := envHolds(yardEnvFile(home, src), input, want)
		if err != nil {
			return source.Holder{}, err
		}
		if held {
			return source.Holder{Yard: true, Source: src.Name}, nil
		}
		for i := range sources {
			if sources[i].Name == src.Name {
				continue
			}
			held, err := envHolds(yardEnvFile(home, &sources[i]), input, want)
			if err != nil {
				return source.Holder{}, err
			}
			if held {
				return source.Holder{Yard: true, Source: sources[i].Name}, nil
			}
		}
	}
	for i := range users {
		if !except.Yard && except.User != "" && users[i].Name == except.User {
			continue
		}
		file, ok := effectiveEnvFile(home, &users[i])
		if !ok {
			continue
		}
		held, err := envHolds(file, input, want)
		if err != nil {
			return source.Holder{}, err
		}
		if held {
			return source.Holder{User: users[i].Name}, nil
		}
	}
	return source.Holder{}, nil
}

// yardEnvFile is the file the source's own credential is read from: the
// one its record names, or the yard's default for its name.
func yardEnvFile(home string, src *Source) string {
	if src.EnvFile != "" {
		return src.EnvFile
	}
	return source.EnvFile(home, src.Name)
}

// envHolds reports whether the env file at path sets input to want
// (already trimmed, matching Credential.Select's TrimSpace).
func envHolds(path, input, want string) (bool, error) {
	f, err := ReadEnvFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(source.Lookup(f.Env, input)) == want, nil
}

// checkBoundValues refuses a giving of values to self where a mint
// input it names is held by another holder: of the mint's inputs it
// asks only for a name some source of the yard declares bound (the
// set as the sources stand in this write), so an input bound nowhere
// passes however widely its value is shared, and one this source
// leaves unbound is refused where another source binds its name. A
// value held by self alone, or by nobody, stands. A static value is
// no mint input and is never asked for. It repeats no value.
func (w *wtx) checkBoundValues(src *Source, d *source.Declaration, self source.Holder, values map[string]string) error {
	if d.Credential == nil {
		return nil
	}
	yardBound, err := w.yardBound()
	if err != nil {
		return err
	}
	for _, name := range d.Credential.Mint {
		if !yardBound[name] {
			continue
		}
		value, given := values[name]
		if !given || strings.TrimSpace(value) == "" {
			continue
		}
		held, err := w.boundHolder(src, name, value, self)
		if err != nil {
			return err
		}
		if !held.IsZero() {
			return boundGiveRefusal(src.Name, name, held, self)
		}
	}
	return nil
}

// boundGiveRefusal says a bound value is held elsewhere and how it is
// freed: the holder gives a different one, and this is given again. It
// names the input and the holder, and never the value. A yard holding
// on another source is named as such and its freeing giving is pointed
// at that source; a holding on this source reads as it always has.
func boundGiveRefusal(srcName, name string, held, self source.Holder) error {
	holder, holderKey := held.User, fmt.Sprintf("yardr source key %s --as %s", srcName, held.User)
	heldOn := ""
	if held.Yard {
		holder, holderKey = "the yard", "yardr source key "+srcName
		if held.Source != "" && held.Source != srcName {
			heldOn = " (on source " + held.Source + ")"
			holderKey = "yardr source key " + held.Source
		}
	}
	selfKey := fmt.Sprintf("yardr source key %s", srcName)
	if !self.Yard {
		selfKey = fmt.Sprintf("yardr source key %s --as %s", srcName, self.User)
	}
	return fmt.Errorf("source %s: %s is held by %s already%s: a bound input belongs to one holder; give a different %s with %s, or have %s give a different %s with %s and give this again", srcName, name, holder, heldOn, name, selfKey, holder, name, holderKey)
}
