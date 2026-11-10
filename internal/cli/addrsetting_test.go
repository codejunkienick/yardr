package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"yardr.dev/yardr/internal/bdp"
	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/web"
)

// The web view's and the BDP server's address are settings of the yard
// (yardr-d4xx): a start and an install with no --addr take web_addr and
// bdp_addr, --addr still wins, and with neither the default stands.
func TestStartAndInstallTakeTheAddrSetting(t *testing.T) {
	// The setting variables are blank in the process already (TestMain).
	home := newHomeDir(t)
	dir, _ := stubServices(t, "darwin")
	st := storetest.New(t)
	// The runs' own environment, shared so a value set below reaches
	// every run; the setting variables are set on the process too,
	// where the setting reader finds them. settings get reads them
	// through store.Setting.Effective from the process, a process fact
	// on design 3.2's list, so this test stays serial.
	runEnv := testEnv(t, home)
	a := func() *app { return &app{st: st, actor: "me", env: runEnv} }
	if _, err := run(t, a().rootCmd(), "init", "--harness", "claude"); err != nil {
		t.Fatal(err)
	}
	var started [][]string
	stub(t, &spawn, func(_ []string, _ *config.Config, k *longRunning, args []string) (int, <-chan error, func() error, error) {
		started = append(started, append(k.command(), args...))
		return 4242, nil, func() error { return nil }, nil
	})
	stub(t, &answers, func(addr string) bool { return len(started) > 0 && slices.Contains(started[len(started)-1], addr) })
	for _, c := range []struct {
		kind, key, def, set, env string
	}{
		{svcWeb, store.WebAddrSetting, "127.0.0.1:8791", "127.0.0.1:9100", "127.0.0.1:9200"},
		{svcBDP, store.BDPAddrSetting, "127.0.0.1:8765", "127.0.0.1:9200", "127.0.0.1:9300"},
	} {
		starts := func(args []string, want string) {
			t.Helper()
			started = nil
			out, err := run(t, a().rootCmd(), append([]string{c.kind, "start"}, args...)...)
			if err != nil || len(started) != 1 || !slices.Equal(started[0], []string{c.kind, "serve", "--addr", want}) {
				t.Fatalf("%s start %v: %v, started %q, want --addr %s", c.kind, args, err, started, want)
			}
			wantText(t, out, "The "+what(c.kind)+": http://"+want+"/")
		}
		installs := func(args []string, want string) {
			t.Helper()
			out, err := run(t, a().rootCmd(), append([]string{c.kind, "install"}, args...)...)
			if err != nil {
				t.Fatalf("%s install %v: %v", c.kind, args, err)
			}
			wantText(t, out, "The "+what(c.kind)+": http://"+want+"/")
			body, err := os.ReadFile(filepath.Join(dir, "dev.yardr."+c.kind+"."+homeID(home)+".plist"))
			if err != nil {
				t.Fatal(err)
			}
			wantText(t, string(body), "<string>--addr</string>\n\t\t<string>"+want+"</string>")
		}
		if got, err := run(t, a().rootCmd(), "settings", "get", c.key); err != nil || got != c.def+"\n" {
			t.Fatalf("settings get %s = %q, %v", c.key, got, err)
		}
		starts(nil, c.def)
		installs(nil, c.def)

		out, err := run(t, a().rootCmd(), "settings", "set", c.key, c.set)
		if err != nil {
			t.Fatal(err)
		}
		// The service installed above keeps its address, and settings
		// set says which and how it gets the new one.
		file := filepath.Join(dir, "dev.yardr."+c.kind+"."+homeID(home)+".plist")
		wantText(t, out, c.key+" = "+c.set+"\n",
			"YARDR_"+strings.ToUpper(c.key)+", if set, takes it instead; otherwise",
			"yardr "+c.kind+" serve, yardr "+c.kind+" start and yardr "+c.kind+" install take it when no --addr is given.",
			"A "+what(c.kind)+" that runs keeps its address until it is stopped and started again: "+hereAt(home, "yardr "+c.kind+" stop")+", then "+hereAt(home, "yardr "+c.kind+" start")+".",
			"The login service keeps the address in its file ("+c.def+", "+file+") until it is installed again: "+hereAt(home, "yardr "+c.kind+" install"))
		if got := serviceAddr(&config.Config{Home: home}, c.kind); got != c.def {
			t.Fatalf("the installed service moved to %q when the setting was set", got)
		}
		starts(nil, c.set)
		starts([]string{"--addr", "127.0.0.1:9300"}, "127.0.0.1:9300")
		installs([]string{"--addr", "127.0.0.1:9300"}, "127.0.0.1:9300")
		// The environment overrides the stored setting, but --addr still
		// overrides both; an install records the effective address too.
		env := "YARDR_" + strings.ToUpper(c.key)
		t.Setenv(env, c.env)
		runEnv[env] = c.env
		starts(nil, c.env)
		starts([]string{"--addr", "127.0.0.1:9400"}, "127.0.0.1:9400")
		installs(nil, c.env)
		if got, err := run(t, a().rootCmd(), "settings", "get", c.key); err != nil || got != c.env+"\n" {
			t.Fatalf("settings get %s under %s = %q, %v", c.key, env, got, err)
		}
		t.Setenv(env, "")
		runEnv[env] = ""
		installs(nil, c.set)
		if _, err := run(t, a().rootCmd(), c.kind, "uninstall"); err != nil {
			t.Fatal(err)
		}
		// With no service there is none to speak of, and unset is the
		// default again.
		out, err = run(t, a().rootCmd(), "settings", "set", c.key, "")
		if err != nil || strings.Contains(out, "login service") {
			t.Fatalf("%v\n%s", err, out)
		}
		starts(nil, c.def)
	}
}

// settings set refuses for web_addr and bdp_addr what --addr refuses, in
// the sentence the flag gives (web.CheckAddr, bdp.CheckAddr).
func TestAddrSettingsAreCheckedAsTheFlag(t *testing.T) {
	home := newHomeDir(t)
	st := storetest.New(t)
	// The runs' own environment, shared so a value set below reaches
	// every run; the setting variables are set on the process too,
	// where the setting reader finds them (design 3.2's list: a test
	// that sets one to a value stays serial).
	env := testEnv(t, home)
	a := func() *app { return &app{st: st, actor: "me", env: env} }
	if _, err := run(t, a().rootCmd(), "init", "--harness", "claude"); err != nil {
		t.Fatal(err)
	}
	stubWeb(t)
	for _, addr := range []string{"0.0.0.0:8791", ":8791", "192.168.1.5:8791", "example.net:8791", "8791"} {
		flag := web.CheckAddr("--addr", addr, false)
		if flag == nil {
			t.Fatalf("--addr %s is taken", addr)
		}
		if _, err := run(t, a().rootCmd(), "web", "start", "--addr", addr); err == nil || !strings.Contains(err.Error(), flag.Error()) {
			t.Fatalf("web start --addr %s = %v", addr, err)
		}
		if _, err := run(t, a().rootCmd(), "settings", "set", "web_addr", addr); err == nil || !strings.Contains(err.Error(), flag.Error()) {
			t.Errorf("settings set web_addr %s = %v, want the flag's: %v", addr, err, flag)
		}
	}
	for _, addr := range []string{"127.0.0.1:9100", "[::1]:9100", "localhost:9100"} {
		if _, err := run(t, a().rootCmd(), "settings", "set", "web_addr", addr); err != nil {
			t.Errorf("settings set web_addr %s: %v", addr, err)
		}
	}
	t.Setenv("YARDR_WEB_ADDR", "192.168.1.5:9100")
	env["YARDR_WEB_ADDR"] = "192.168.1.5:9100"
	if _, err := run(t, a().rootCmd(), "web", "start"); err == nil || !strings.Contains(err.Error(), "never listen off loopback") {
		t.Fatalf("web start under non-loopback YARDR_WEB_ADDR = %v", err)
	}
	t.Setenv("YARDR_WEB_ADDR", "")
	env["YARDR_WEB_ADDR"] = ""
	for _, addr := range []string{"0.0.0.0:8765", ":8765", "192.168.1.5:8765", "example.net:8765", "8765"} {
		flag := bdp.CheckAddr("--addr", addr, false)
		if flag == nil {
			t.Fatalf("--addr %s is taken", addr)
		}
		if _, err := run(t, a().rootCmd(), "settings", "set", "bdp_addr", addr); err == nil || !strings.Contains(err.Error(), flag.Error()) {
			t.Errorf("settings set bdp_addr %s = %v, want the flag's: %v", addr, err, flag)
		}
	}
	for _, addr := range []string{"127.0.0.1:9100", "[::1]:9100", "localhost:9100"} {
		if _, err := run(t, a().rootCmd(), "settings", "set", "bdp_addr", addr); err != nil {
			t.Errorf("settings set bdp_addr %s: %v", addr, err)
		}
	}
}

// freeAddr is a loopback address nothing listens on, as far as one can be
// had: it was free a moment ago, and not for long. A connection another
// process opens is given its source port from the pool this one came out of,
// as is a listener that asks for port 0, so the address can be taken between
// the release here and the listen of whatever is handed it. The listen of a
// serve on it goes through onFreeAddrs, which tries again on another
// (yardr-eenj).
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// addrTries is how many sets of free addresses onFreeAddrs spends before it
// calls the refusals a fault. A port taken by chance is seldom taken again on
// the next set, so refusals five times in a row are a cause that stays: a
// serve that listens twice on one address, a listener it leaves bound.
const addrTries = 5

// onFreeAddrs runs try, which takes the addresses it needs from free and
// serves on them. Where try ends at a listen refused as in use on an address
// free gave, something took the port between its release and the listen, and
// try runs again on fresh addresses. A serve takes an address and not a
// listener (web.Listen, bdp.Listen), so no test can hold the port up to the
// listen: this is what is left. try returns nil when it passed, or that
// refusal; any other failure it reports to t itself. A refusal on an address
// free did not give (the default origin's port, held on purpose) fails the
// test at once with the error as it was, and so do addrTries refusals in a
// row.
func onFreeAddrs(t *testing.T, try func(free func() string) error) {
	t.Helper()
	var err error
	for range addrTries {
		var given []string
		err = try(func() string {
			addr := freeAddr(t)
			given = append(given, addr)
			return addr
		})
		if err == nil {
			return
		}
		if !refusedAt(err, given) {
			t.Fatal(err)
		}
	}
	t.Fatalf("%d sets of free addresses in a row were refused as in use: %v", addrTries, err)
}

// refusedAt says whether err is a listen on one of addrs that was refused as
// in use. The address is read from the error, not searched for in its text,
// for a serve's message names its arguments as well.
func refusedAt(err error, addrs []string) bool {
	var op *net.OpError
	return errors.Is(err, syscall.EADDRINUSE) && errors.As(err, &op) &&
		op.Op == "listen" && op.Addr != nil && slices.Contains(addrs, op.Addr.String())
}

// web and BDP serve take the setting, environment or flag in precedence
// order, listen on it, and report the address in status.
func TestServeTakesTheAddrSetting(t *testing.T) {
	// YARDR_WEB_AS is blank in the process already (TestMain).
	home, st, _ := statusYard(t, "abcdef123456")
	cfg := &config.Config{Home: home}
	a := func() *app { return &app{st: st, cfg: cfg, actor: "me"} }
	for _, c := range []struct{ kind, key string }{{svcWeb, store.WebAddrSetting}, {svcBDP, store.BDPAddrSetting}} {
		// serves is one run of the serve, wanted on want. Where the serve
		// ends at a listen refused as in use it returns that error, for
		// onFreeAddrs to judge; it fails the test on anything else.
		serves := func(args []string, want string) error {
			t.Helper()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := a().rootCmd()
			serveArgs := append([]string{c.kind, "serve"}, args...)
			if c.kind == svcWeb {
				// The sandbox origin beside the view: off a fixed
				// port, so the test holds no address of the
				// machine's.
				serveArgs = append(serveArgs, "--pages-addr", "127.0.0.1:0")
			}
			cmd.SetArgs(serveArgs)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			k := kindOf(c.kind)
			var up []instance
			for deadline := time.Now().Add(10 * time.Second); len(up) == 0; time.Sleep(10 * time.Millisecond) {
				select {
				case err := <-done:
					if errors.Is(err, syscall.EADDRINUSE) {
						return fmt.Errorf("%s serve %v ended: %w", c.kind, args, err)
					}
					t.Fatalf("%s serve %v ended: %v", c.kind, args, err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s serve %v recorded nothing", c.kind, args)
				}
				up = k.running(cfg)
			}
			if up[0].Addr != want {
				t.Errorf("%s serve listens on %s, want %s", c.kind, up[0].Addr, want)
			}
			if conn, err := net.DialTimeout("tcp", want, 5*time.Second); err != nil {
				t.Errorf("nothing listens on %s: %v", want, err)
			} else {
				conn.Close()
			}
			out, err := run(t, a().rootCmd(), c.kind, "status")
			if err != nil {
				t.Fatal(err)
			}
			wantText(t, out, " at http://"+want+"/\n")
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s serve did not stop", c.kind)
			}
			return nil
		}
		// The setting, then the environment, then the flag: each stays given
		// when the next is, so each is shown to beat the ones before it. A
		// serve that was refused its address is tried on a fresh one, given
		// again where the serve reads it.
		for _, give := range []func(addr string) []string{
			func(addr string) []string {
				if err := st.SetSetting(context.Background(), c.key, addr, "Jo"); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			func(addr string) []string {
				if c.kind == svcWeb {
					cfg.WebAddr = addr
				} else {
					cfg.BDPAddr = addr
				}
				return nil
			},
			func(addr string) []string { return []string{"--addr", addr} },
		} {
			onFreeAddrs(t, func(free func() string) error {
				addr := free()
				return serves(give(addr), addr)
			})
		}
	}
}
