package cli

import (
	"context"
	"strings"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// boundHolderFor is the holder lookup source check --as holds a bound
// input to: who besides except holds each bound value the user's file
// names, delegating to store.BoundHolderIn so the CLI and store share
// one implementation. The yard's home is read only when a non-skipped
// holder has no env_file recorded and needs the default path: any
// source's yard file without one needs it, as any user's does.
func (a *app) boundHolderFor(ctx context.Context, st store.API, src *store.Source) source.BoundHolder {
	return func(input, value string, except source.Holder) (source.Holder, error) {
		if strings.TrimSpace(value) == "" {
			return source.Holder{}, nil
		}
		users, err := st.Users(ctx)
		if err != nil {
			return source.Holder{}, err
		}
		sources, err := st.Sources(ctx)
		if err != nil {
			return source.Holder{}, err
		}
		needHome := false
		if !except.Yard {
			if src.EnvFile == "" {
				needHome = true
			}
			for i := range sources {
				if sources[i].EnvFile == "" {
					needHome = true
					break
				}
			}
		}
		if !needHome {
			for i := range users {
				if !except.Yard && except.User != "" && users[i].Name == except.User {
					continue
				}
				// Matches store.userEnvFile: names with path separators get no default file.
				if users[i].EnvFile == "" && !strings.ContainsAny(users[i].Name, `/\\`) {
					needHome = true
					break
				}
			}
		}
		var home string
		if needHome {
			cfg, err := a.configReadOnly()
			if err != nil {
				return source.Holder{}, err
			}
			home = cfg.Home
		}
		return store.BoundHolderIn(home, src, sources, users, input, value, except)
	}
}
