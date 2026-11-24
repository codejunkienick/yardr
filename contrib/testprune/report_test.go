package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// alone is one alone run as the tool measured it: the test's wall seconds
// and the lines it covered, one statement each. The seconds in these tests
// are binary fractions, so every sum is exact and a comparison needs no
// tolerance.
func alone(name string, seconds float64, lines ...int) testResult {
	return testResult{
		name: name, seconds: seconds, outcome: outcomePass,
		covered: set(lines...), statements: len(lines),
	}
}

// times is n alone runs of s seconds each.
func times(n int, s float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestStartFloorOf(t *testing.T) {
	for _, c := range []struct {
		name    string
		seconds []float64
		floor   float64
		runs    int
	}{
		{"no runs", nil, 0, 0},
		{"one run", []float64{3}, 3, 1},
		{"an odd count reads the middle run", []float64{5, 1, 3}, 3, 3},
		{"an even count reads the mean of the middle two", []float64{4, 1, 3, 2}, 2.5, 4},
		// Reading all 25 would put the median at 2 s.
		{"only the cheapest 20 are read", slices.Concat(times(12, 1), times(8, 2), times(5, 50)), 1, 20},
		// The single cheapest run would put the floor at 0.0625 s.
		{"a run that ended early does not set it", slices.Concat(times(10, 0.25), []float64{0.0625}, times(14, 0.25)), 0.25, 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			var results []testResult
			for i, s := range c.seconds {
				results = append(results, alone(fmt.Sprintf("Test%02d", i), s))
			}
			floor, runs := startFloorOf(results)
			if floor != c.floor || runs != c.runs {
				t.Fatalf("floor %v from %d runs, want %v from %d", floor, runs, c.floor, c.runs)
			}
		})
	}
}

func TestReportNetSeconds(t *testing.T) {
	// The 20 cheapest runs read a floor of 0.25 s: eighteen tests that
	// each own a line and take exactly that, TestOutlier, which owns a line
	// and ended early at 0.0625 s, and TestQuick, also under the floor,
	// whose one line TestKept covers too. TestSlow and TestQuick are the
	// removal set: TestSlow costs 4 s net, TestQuick nothing.
	results := []testResult{
		alone("TestOutlier", 0.0625, 200),
		alone("TestQuick", 0.0625, 3),
		alone("TestKept", 1.25, 1, 2, 3),
		alone("TestSlow", 4.25, 1, 2),
	}
	for i := 1; i <= 18; i++ {
		results = append(results, alone(fmt.Sprintf("TestStart%02d", i), 0.25, 100+i))
	}
	rep := analyze("./p", "yardr.dev/yardr/p", results, 200, 100, 9)

	if !slices.Equal(rep.removal, []string{"TestSlow", "TestQuick"}) {
		t.Fatalf("removal %v, want TestSlow then TestQuick", rep.removal)
	}
	if rep.startFloor != 0.25 || rep.floorRuns != 20 {
		t.Fatalf("floor %v from %d runs, want 0.25 from 20", rep.startFloor, rep.floorRuns)
	}

	dir := t.TempDir()
	mdPath, jsPath := filepath.Join(dir, "testprune.md"), filepath.Join(dir, "testprune.json")
	if err := writeReport(rep, mdPath, jsPath); err != nil {
		t.Fatal(err)
	}

	// The JSON is read by key, as the scripts that read a report read it.
	raw, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]float64{
		"startFloorSeconds": 0.25,
		"startFloorRuns":    20,
		"removalSeconds":    4.3125,
		"removalNetSeconds": 4,
		"testsSeconds":      10.125,
		"testsNetSeconds":   5,
	} {
		if got := top[key]; got != want {
			t.Errorf("json %s = %v, want %v", key, got, want)
		}
	}
	// A test under the floor is at 0, not below it, so it adds nothing to a
	// combined figure.
	gotNet := map[string]any{}
	rows, _ := top["tests"].([]any)
	for _, v := range rows {
		row, _ := v.(map[string]any)
		name, _ := row["name"].(string)
		gotNet[name] = row["netSeconds"]
	}
	if len(gotNet) != len(results) {
		t.Fatalf("json holds %d tests, want %d", len(gotNet), len(results))
	}
	for name, want := range map[string]float64{
		"TestSlow": 4, "TestKept": 1, "TestQuick": 0, "TestOutlier": 0, "TestStart01": 0,
	} {
		if gotNet[name] != want {
			t.Errorf("json %s netSeconds = %v, want %v", name, gotNet[name], want)
		}
	}

	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Start floor 0.25 s (median of cheapest 20).",
		"22 alone runs 10.1 s, 5.0 s net.",
		"Candidates: 2 tests, 4.0 s net, 4.3 s as run alone.",
		"| test | time (s) | net (s) | statements |",
		"| TestSlow | 4.25 | 4.00 | 2 |",
		"| TestQuick | 0.06 | 0.00 | 1 |",
		"| test | outcome | time (s) | net (s) | statements | unique | rm |",
		"| TestKept | pass | 1.25 | 1.00 | 3 | 0 |",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
