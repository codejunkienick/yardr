package mcp_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"yardr.dev/yardr/internal/mcp"
)

// dumpGolden names a directory to write the edge's MCP golden into (the
// contract's tools and words, as the router's testdata holds them) instead
// of running the tests. Regenerating the golden is one command against a
// yardr checkout at the pin:
//
//	go test ./internal/mcp -run XXX -args -dump-golden <dir>
//
// The ritual the golden serves is the header of the router's
// mcpcontract.go: regenerate both files from the new Tools() and name the
// commit there. The bytes are the throwaway generator's (yardr-hosted-s9w9
// note 28531) and must stay them: the parity tests forbid drift.
var dumpGolden = flag.String("dump-golden", "", "write mcp-tools.json and mcp-words.json to `dir` and exit without running tests")

func TestMain(m *testing.M) {
	flag.Parse()
	if *dumpGolden != "" {
		os.Exit(dumpGoldens(*dumpGolden))
	}
	os.Exit(m.Run())
}

// dumpGoldens writes the golden pair to dir in the throwaway's exact bytes:
// Tools() as tab-indented JSON with a trailing newline, and the words map
// ({instructions, floor_version, floor_message, default_wait_s,
// short_wait_s}, sorted keys) the same way.
func dumpGoldens(dir string) int {
	tools, err := json.MarshalIndent(mcp.Tools(), "", "\t")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp-tools.json"), append(tools, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	words, err := json.MarshalIndent(map[string]any{
		"instructions":   mcp.Instructions,
		"floor_version":  mcp.FloorVersion,
		"floor_message":  mcp.FloorMessage,
		"default_wait_s": int(mcp.DefaultWait.Seconds()),
		"short_wait_s":   int(mcp.ShortWait.Seconds()),
	}, "", "\t")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp-words.json"), append(words, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
