package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// copyRepoFiles makes a directory for a dir depot whose .yardr/flows holds
// files: the keys are paths there (default.flow, type/wagon.flow,
// default/new.md).
func copyRepoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, store.RepoFlowDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// flow copy-repo copies the flows of a depot's repository into the store and
// says, file by file, what became which flow, what stayed, and how to point
// each pointer back, in the order that works; with --json it is the store's
// answer alone. It refuses what the store refuses, in the store's words, and
// is the operator's alone.
func TestFlowCopyRepo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	st := storetest.NewIn(t, home)
	ctx := context.Background()
	dir := copyRepoFiles(t, map[string]string{
		"default.flow": "backlog -> new\nnew -> done\ndone terminal\n", "type/wagon.flow": "wait -> ride\nride -> done\ndone terminal\n",
		"default/new.md": "Build it.\n", "default/backlog.md": "Wait here.\n",
	})
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Kind: store.DepotDir, Path: dir}, "me"); err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, actor: "me"}
	copyRepo := func(args ...string) (string, error) {
		t.Helper()
		return run(t, a.flowCmd(), append([]string{"copy-repo"}, args...)...)
	}

	out, err := copyRepo("web")
	if err != nil {
		t.Fatal(err)
	}
	want := "copied the flows of depot web into the store, from repo (directory):\n" +
		"  .yardr/flows/default.flow -> web.default (2 stage files)\n" +
		"  .yardr/flows/type/wagon.flow -> web.wagon (0 stage files)\n" +
		"to point back at the repository, in this order:\n" +
		"  yardr depot source web flow depot\n" +
		"  yardr depot source web flow --type wagon depot\n"
	if out != want {
		t.Errorf("flow copy-repo printed\n%s\nwant\n%s", out, want)
	}
	if sf, err := store.HomeStageFile(home, "web.default", "new"); err != nil || sf == nil || sf.Text != "Build it.\n" {
		t.Errorf("the stage file of new in web.default is %+v, %v", sf, err)
	}
	ptrs, err := st.FlowPointers(ctx)
	if want := []store.FlowPointer{{Depot: "web", Flow: "web.default"}, {Depot: "web", Type: "wagon", Flow: "web.wagon"}}; err != nil || !reflect.DeepEqual(ptrs, want) {
		t.Errorf("the pointers are %+v, %v, want %+v", ptrs, err, want)
	}

	// Nothing is left to copy, and nothing else is the command's to refuse.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"web"}, "every flow that governs depot web is in the store already (default (flow web.default), type/wagon (flow web.wagon))"},
		{[]string{"ghost"}, "depot ghost: not found"},
		{nil, "accepts 1 arg(s), received 0"},
		{[]string{"web", "extra"}, "accepts 1 arg(s), received 2"},
	} {
		if _, err := copyRepo(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("flow copy-repo %v = %v, want %q", c.args, err, c.want)
		}
	}

	// Pointed back, the copy stays and the next copy says it replaces it,
	// and that a flow already the store's stays as it is.
	for _, typ := range []string{"", "wagon"} {
		if err := st.SetFlowPointer(ctx, "web", typ, "", "me"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetFlowPointer(ctx, "web", "wagon", "web.wagon", "me"); err != nil {
		t.Fatal(err)
	}
	a.json = true
	out, err = copyRepo("web")
	a.json = false
	var got store.CopiedFlows
	if err != nil || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("flow copy-repo --json = %q, %v", out, err)
	}
	wantJSON := store.CopiedFlows{Depot: "web", Source: "repo (directory)", Kept: []string{"type/wagon"}, Flows: []store.CopiedFlow{
		{From: ".yardr/flows/default.flow", Flow: "web.default", StageFiles: 2, Replaced: true, Back: "yardr depot source web flow depot"}}}
	if !reflect.DeepEqual(got, wantJSON) {
		t.Errorf("flow copy-repo --json = %+v, want %+v", got, wantJSON)
	}
	if err := st.SetFlowPointer(ctx, "web", "", "", "me"); err != nil {
		t.Fatal(err)
	}
	out, err = copyRepo("web")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  .yardr/flows/default.flow -> web.default (2 stage files, replacing an unused earlier copy)\n",
		"left as they are, in the store already: type/wagon\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("flow copy-repo printed\n%s\nwant it to say %q", out, want)
		}
	}

	// The operator's alone, once the yard has one.
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFlowPointer(ctx, "web", "", "", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyRepo("web"); err == nil || !strings.Contains(err.Error(), "only the operator (jo) may change a flow's stages; this is me") {
		t.Errorf("flow copy-repo as someone else = %v, want it the operator's alone", err)
	}
	if ptrs, err := st.FlowPointers(ctx); err != nil || len(ptrs) != 1 {
		t.Errorf("a refused copy left the pointers %+v, %v", ptrs, err)
	}
	a.actor = "jo"
	if _, err := copyRepo("web"); err != nil {
		t.Errorf("flow copy-repo as the operator: %v", err)
	}
}
