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

// A bound input of a source (a mint input its declaration marks bound)
// belongs to one holder: the yard, in the source's own env file, or one
// user, in theirs. Giving refuses a bound value another holder has, and
// a call made as a user refuses one its file names from another holder,
// so a file edited by hand is caught as a given one is. The value itself
// is never kept or shown: only who holds it is answered.

// BoundHolder answers which holder other than except holds value of the
// bound input input of the source sourceName: the yard, a user, or zero
// where no other holder holds it. Pass a zero source.Holder{} for any
// holder, or the acting holder to check whether anyone else holds it.
// It reads the source's own env file and every user's, as they stand.
// It is the helper a pasted sign-in asks before it writes (yardr-yt5k),
// beside the givings and calls here.
func (s *Store) BoundHolder(ctx context.Context, sourceName, input, value string, except source.Holder) (source.Holder, error) {
	var src *Source
	var users []User
	err := s.read(func(tx *bolt.Tx) error {
		src = new(Source)
		if ok, err := get(tx, bSources, sourceName, src); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("source %s: %w", sourceName, ErrNotFound)
		}
		return each(tx, bUsers, "", func(_ []byte, u *User) error {
			users = append(users, *u)
			return nil
		})
	})
	if err != nil {
		return source.Holder{}, err
	}
	return BoundHolderIn(s.home, src, users, input, value, except)
}

// boundHolder is BoundHolder inside a write, from its users as they stand.
func (w *wtx) boundHolder(src *Source, input, value string, except source.Holder) (source.Holder, error) {
	var users []User
	if err := each(w.Tx, bUsers, "", func(_ []byte, u *User) error {
		users = append(users, *u)
		return nil
	}); err != nil {
		return source.Holder{}, err
	}
	return BoundHolderIn(w.home, src, users, input, value, except)
}

// boundLookup is a BoundHolder for the calls of src: which holder holds
// each bound value a call as a user names, read as the files stand.
func (s *Store) boundLookup(src *Source) source.BoundHolder {
	return func(input, value string, except source.Holder) (source.Holder, error) {
		var users []User
		err := s.read(func(tx *bolt.Tx) error {
			return each(tx, bUsers, "", func(_ []byte, u *User) error {
				users = append(users, *u)
				return nil
			})
		})
		if err != nil {
			return source.Holder{}, err
		}
		return BoundHolderIn(s.home, src, users, input, value, except)
	}
}

// BoundHolderIn is who holds value of input of src among the yard (its
// own file first) and users, in slice order, skipping except (zero skips
// nobody). Skipping except matters when two holders share a value (by
// hand or before binding): stopping at self first would miss a second
// holder who sorts later. An empty value is held by nobody. A file that
// is not there holds nothing; one that cannot be read fails, since it
// may hold the value unseen. Both the store and yardr source check --as
// call this one helper so their rules stay identical.
func BoundHolderIn(home string, src *Source, users []User, input, value string, except source.Holder) (source.Holder, error) {
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
			return source.Holder{Yard: true}, nil
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

// checkBoundValues refuses a giving of values to self where a bound
// input it names is held by another holder. d names the bound inputs; a
// value held by self alone, or by nobody, stands. It repeats no value.
func (w *wtx) checkBoundValues(src *Source, d *source.Declaration, self source.Holder, values map[string]string) error {
	if d.Credential == nil {
		return nil
	}
	for _, name := range d.Credential.Bound {
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
// names the input and the holder, and never the value.
func boundGiveRefusal(srcName, name string, held, self source.Holder) error {
	holder, holderKey := held.User, fmt.Sprintf("yardr source key %s --as %s", srcName, held.User)
	if held.Yard {
		holder, holderKey = "the yard", "yardr source key "+srcName
	}
	selfKey := fmt.Sprintf("yardr source key %s", srcName)
	if !self.Yard {
		selfKey = fmt.Sprintf("yardr source key %s --as %s", srcName, self.User)
	}
	return fmt.Errorf("source %s: %s is held by %s already: a bound input belongs to one holder; give a different %s with %s, or have %s give a different %s with %s and give this again", srcName, name, holder, name, selfKey, holder, name, holderKey)
}
