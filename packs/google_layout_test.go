package packs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// tableInsert is one cell text of the table call's fill, with its
// hand-counted index.
type tableInsert struct {
	index int
	text  string
}

// The table call builds its fill per call. Its build script reads the
// document, finds the one place the placeholder stands, sends the place
// itself - a deleteContentRange of the placeholder and an insertTable at
// its start, pinned to the revision read - reads again for the new
// revision and to see the table where it put it, and prints the fill: an
// insertText per non-empty cell, from the last cell backwards, and with
// borders=false one updateTableCellStyle taking every border away, each
// border sent whole. The table stands one index past the placeholder, a
// newline before it, and an empty cell runs two units, so the fill's
// indices follow from the placeholder's start alone. Every index below is
// counted by hand from the document the stand-in serves and the Tables
// how-to's index table, not worked out the way the script does. No test
// calls Google.
func TestGoogleDocsTableBuilds(t *testing.T) {
	// A border taken away carries every field of TableCellBorder: the
	// width of 0 that hides it, an opaque color, since a table cell
	// border cannot be transparent and an unset color is, and the dash
	// style the reference gives as the default. The golden below is
	// compared whole, so a field dropped from the script fails here.
	noborder := `{"color":{"color":{"rgbColor":{"red":0,"green":0,"blue":0}}},"width":{"magnitude":0,"unit":"PT"},"dashStyle":"SOLID"}`
	for _, tc := range []struct {
		name    string
		doc     string // the stand-in's doc
		find    string
		cells   string // the cells argument, literal JSON
		borders string // "" leaves borders out
		tab     string // the tab the placeholder stands in
		start   int    // the placeholder's start
		end     int    // the placeholder's end
		rows    int
		cols    int
		// The fill's cell texts in the order the batch sends them,
		// last cell first, each with its hand-counted index.
		inserts []tableInsert
		style   bool // the fill styles the whole table borderless
	}{
		{name: "two rows and columns", doc: "one", find: "{{logo}}", cells: `[["Item","CHF"],["Bread","1.20"]]`,
			tab: "t.0", start: 7, end: 15, rows: 2, cols: 2,
			inserts: []tableInsert{{18, "1.20"}, {16, "Bread"}, {13, "CHF"}, {11, "Item"}}},
		{name: "without borders", doc: "one", find: "{{logo}}", cells: `[["Item","CHF"],["Bread","1.20"]]`, borders: "false",
			tab: "t.0", start: 7, end: 15, rows: 2, cols: 2, style: true,
			inserts: []tableInsert{{18, "1.20"}, {16, "Bread"}, {13, "CHF"}, {11, "Item"}}},
		{name: "with borders named", doc: "one", find: "{{logo}}", cells: `[["Item","CHF"],["Bread","1.20"]]`, borders: "true",
			tab: "t.0", start: 7, end: 15, rows: 2, cols: 2,
			inserts: []tableInsert{{18, "1.20"}, {16, "Bread"}, {13, "CHF"}, {11, "Item"}}},
		{name: "one cell holding a placeholder", doc: "one", find: "{{logo}}", cells: `[["{{price_1}}"]]`,
			tab: "t.0", start: 7, end: 15, rows: 1, cols: 1,
			inserts: []tableInsert{{11, "{{price_1}}"}}},
		{name: "an empty cell is skipped", doc: "one", find: "{{logo}}", cells: `[["a","","c"]]`,
			tab: "t.0", start: 7, end: 15, rows: 1, cols: 3,
			inserts: []tableInsert{{15, "c"}, {11, "a"}}},
		{name: "a cell of two lines", doc: "one", find: "{{logo}}", cells: `[["a\nb"]]`,
			tab: "t.0", start: 7, end: 15, rows: 1, cols: 1,
			inserts: []tableInsert{{11, "a\nb"}}},
		{name: "a placeholder split by a style change", doc: "split", find: "{{logo}}", cells: `[["x"]]`,
			tab: "t.0", start: 7, end: 15, rows: 1, cols: 1,
			inserts: []tableInsert{{11, "x"}}},
		{name: "an emoji before it counts double", doc: "emoji", find: "{{logo}}", cells: `[["A","B"]]`,
			tab: "t.0", start: 10, end: 18, rows: 1, cols: 2,
			inserts: []tableInsert{{16, "B"}, {14, "A"}}},
		{name: "a placeholder in a child tab", doc: "second", find: "{{logo}}", cells: `[["x"]]`,
			tab: "t.1.1", start: 1, end: 9, rows: 1, cols: 1,
			inserts: []tableInsert{{5, "x"}}},
		{name: "a placeholder in a table cell", doc: "cell", find: "{{logo}}", cells: `[["n"]]`,
			tab: "t.0", start: 10, end: 18, rows: 1, cols: 1,
			inserts: []tableInsert{{14, "n"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			params := map[string]string{"docId": standin.doc(tc.doc), "find": tc.find, "cells": tc.cells}
			if tc.borders != "" {
				params["borders"] = tc.borders
			}
			body, err := source.BuildBody(context.Background(), docs, "table", params, "tok", layoutHome(t), store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			revisions := map[string][2]string{
				"one": {"rev-one", "rev-one-placed"}, "split": {"rev-split", "rev-split-placed"},
				"emoji": {"rev-emoji", "rev-emoji-placed"}, "cell": {"rev-cell", "rev-cell-placed"},
				"second": {"rev-second", "rev-second-placed"},
			}
			rev1, rev2 := revisions[tc.doc][0], revisions[tc.doc][1]
			var reqs []string
			if tc.style {
				reqs = append(reqs, `{"updateTableCellStyle":{"tableStartLocation":{"index":`+strconv.Itoa(tc.start+1)+
					`,"tabId":`+strconv.Quote(tc.tab)+`},"tableCellStyle":{"borderTop":`+noborder+
					`,"borderBottom":`+noborder+`,"borderLeft":`+noborder+`,"borderRight":`+noborder+`},`+
					`"fields":"borderTop,borderBottom,borderLeft,borderRight"}}`)
			}
			for _, in := range tc.inserts {
				reqs = append(reqs, `{"insertText":{"location":{"index":`+strconv.Itoa(in.index)+
					`,"tabId":`+strconv.Quote(tc.tab)+`},"text":`+strconv.Quote(in.text)+`}}`)
			}
			golden := `{"requests":[` + strings.Join(reqs, ",") + `],"writeControl":{"requiredRevisionId":` + strconv.Quote(rev2) + `}}`
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("built body = %s: %v", body, err)
			}
			if err := json.Unmarshal([]byte(golden), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("built body = %s, want %s", body, golden)
			}
			// The place the script sent itself, between its two reads:
			// the placeholder deleted and the table at its start, pinned
			// to the first revision.
			gotReqs := standin.requests()
			if len(gotReqs) != 3 {
				t.Fatalf("the stand-in heard %d requests, want the pre-read, the place and the second read", len(gotReqs))
			}
			pre := gotReqs[0]
			if pre.method != "GET" || pre.path != "/v1/documents/"+standin.doc(tc.doc) {
				t.Fatalf("pre-read = %s %s", pre.method, pre.path)
			}
			if pre.auth != "Bearer tok" {
				t.Fatalf("pre-read Authorization = %q, want the call's credential", pre.auth)
			}
			if pre.query.Get("fields") != "revisionId,tabs" || pre.query.Get("includeTabsContent") != "true" {
				t.Fatalf("pre-read query = %v", pre.query)
			}
			place := gotReqs[1]
			if place.method != "POST" || place.path != "/v1/documents/"+standin.doc(tc.doc)+":batchUpdate" {
				t.Fatalf("place = %s %s", place.method, place.path)
			}
			if place.auth != "Bearer tok" {
				t.Fatalf("place Authorization = %q, want the call's credential", place.auth)
			}
			if place.contentType != "application/json" {
				t.Fatalf("place Content-Type = %q, want application/json", place.contentType)
			}
			placeGolden := `{"requests":[{"deleteContentRange":{"range":{"startIndex":` + strconv.Itoa(tc.start) +
				`,"endIndex":` + strconv.Itoa(tc.end) + `,"tabId":` + strconv.Quote(tc.tab) + `}}},` +
				`{"insertTable":{"location":{"index":` + strconv.Itoa(tc.start) + `,"tabId":` + strconv.Quote(tc.tab) +
				`},"rows":` + strconv.Itoa(tc.rows) + `,"columns":` + strconv.Itoa(tc.cols) + `}}],` +
				`"writeControl":{"requiredRevisionId":` + strconv.Quote(rev1) + `}}`
			var gotPlace, wantPlace any
			if err := json.Unmarshal(place.body, &gotPlace); err != nil {
				t.Fatalf("placed body = %s: %v", place.body, err)
			}
			if err := json.Unmarshal([]byte(placeGolden), &wantPlace); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotPlace, wantPlace) {
				t.Fatalf("placed body = %s, want %s", place.body, placeGolden)
			}
			second := gotReqs[2]
			if second.method != "GET" || second.path != "/v1/documents/"+standin.doc(tc.doc) {
				t.Fatalf("second read = %s %s", second.method, second.path)
			}
			if second.auth != "Bearer tok" {
				t.Fatalf("second read Authorization = %q, want the call's credential", second.auth)
			}
			if second.query.Get("fields") != "revisionId,tabs" || second.query.Get("includeTabsContent") != "true" {
				t.Fatalf("second read query = %v", second.query)
			}
		})
	}
}

// What the table call refuses to build: nothing is printed for the yard to
// send, and the caller reads the script's own words. The stand-in hears the
// pre-read where the refusal needed the document, and the place where the
// refusal came past it; where the refusal is of the parameters alone, it
// hears nothing at all. Past the place the empty table stands, so those
// refusals say to remove it, restore the placeholder, and call table again.
func TestGoogleDocsTableRefusals(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	manyRows := "[" + strings.TrimSuffix(strings.Repeat(`["x"],`, 21), ",") + "]"
	longCell := `[["` + strings.Repeat("x", 2001) + `"]]`
	placeOf := func(rev string) string {
		return `{"requests":[{"deleteContentRange":{"range":{"startIndex":7,"endIndex":15,"tabId":"t.0"}}},` +
			`{"insertTable":{"location":{"index":7,"tabId":"t.0"},"rows":2,"columns":2}}],` +
			`"writeControl":{"requiredRevisionId":` + strconv.Quote(rev) + `}}`
	}
	for _, tc := range []struct {
		name   string
		doc    string
		params map[string]string
		words  string
		heard  int    // requests heard past before: the pre-read, the place, the second read
		place  string // the place the refusal came past, "" for none
	}{
		{"cells are no JSON", "one", map[string]string{"find": "{{logo}}", "cells": "nope"}, "cells is no JSON array of rows, each an array of cell texts", 0, ""},
		{"cells are no array", "one", map[string]string{"find": "{{logo}}", "cells": "3"}, "cells is no JSON array of rows, each an array of cell texts", 0, ""},
		{"cells hold no row", "one", map[string]string{"find": "{{logo}}", "cells": "[]"}, "cells holds no row to put", 0, ""},
		{"a row is no array", "one", map[string]string{"find": "{{logo}}", "cells": `["x"]`}, "row 1 is no array of cell texts", 0, ""},
		{"a row holds no cell", "one", map[string]string{"find": "{{logo}}", "cells": "[[]]"}, "row 1 holds no cell", 0, ""},
		{"rows of unequal length", "one", map[string]string{"find": "{{logo}}", "cells": `[["a"],["b","c"]]`}, "row 2 holds 2 cells, want 1 like the first row", 0, ""},
		{"too many rows", "one", map[string]string{"find": "{{logo}}", "cells": manyRows}, "cells holds 21 rows, and a table holds at most 20", 0, ""},
		{"too many columns", "one", map[string]string{"find": "{{logo}}", "cells": `[["a","b","c","d","e","f","g"]]`}, "row 1 holds 7 cells, and a table holds at most 6 columns", 0, ""},
		{"a cell is no text", "one", map[string]string{"find": "{{logo}}", "cells": `[["a",7]]`}, "row 1 cell 2 is no text", 0, ""},
		{"a cell too long", "one", map[string]string{"find": "{{logo}}", "cells": longCell}, "row 1 cell 1 is 2001 characters, and at most 2000", 0, ""},
		{"a cell with a control character", "one", map[string]string{"find": "{{logo}}", "cells": "[[\"a\\u0001b\"]]"}, "row 1 cell 1 holds a control or private-use character Google Docs strips", 0, ""},
		{"a cell with NUL", "one", map[string]string{"find": "{{logo}}", "cells": "[[\"a\\u0000b\"]]"}, "row 1 cell 1 holds a control or private-use character Google Docs strips", 0, ""},
		{"every cell empty", "one", map[string]string{"find": "{{logo}}", "cells": `[["",""]]`}, "cells holds no text: every cell is empty", 0, ""},
		{"an empty find", "one", map[string]string{"find": "", "cells": `[["a"]]`}, "find names no placeholder to put the table in the place of", 0, ""},
		{"a find with a line break", "one", map[string]string{"find": "Lunch\nFriday", "cells": `[["a"]]`}, "find holds a line break: a placeholder is one line of text", 0, ""},
		{"an unknown placeholder", "one", map[string]string{"find": "{{nope}}", "cells": `[["a"]]`}, "no match: find is in no text of the body", 1, ""},
		{"a placeholder of another case", "one", map[string]string{"find": "{{LOGO}}", "cells": `[["a"]]`}, "no match: find is in no text of the body", 1, ""},
		{"a placeholder only in a header", "headed", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "no match: find is in no text of the body", 1, ""},
		{"a placeholder cut by an inline picture", "gap", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "no match: find is in no text of the body", 1, ""},
		{"a second match", "twice", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "2 matches of find in the body: a table goes in one place, so make find match exactly one", 1, ""},
		{"a second match in another tab", "twotabs", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "2 matches of find in the body: a table goes in one place, so make find match exactly one", 1, ""},
		{"no such doc", "missing", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "could not be read", 1, ""},
		{"no revision id", "no-revision", map[string]string{"find": "Lunch", "cells": `[["a"]]`}, "answered no revision id", 1, ""},
		{"no tabs", "no-tabs", map[string]string{"find": "Lunch", "cells": `[["a"]]`}, "answered no tabs", 1, ""},
		{"runs that overlap", "overlap", map[string]string{"find": "{{logo}}", "cells": `[["a"]]`}, "answered text runs whose indices overlap", 1, ""},
		{"the place fails", "reject", map[string]string{"find": "{{logo}}", "cells": `[["a","b"],["c","d"]]`}, "the table could not be placed in document", 2, placeOf("rev-reject")},
		{"the second read finds no table", "plain", map[string]string{"find": "{{logo}}", "cells": `[["a","b"],["c","d"]]`}, "the table was placed but a second read shows no 2x2 table at index 8: remove the empty table, restore the placeholder, and call table again", 3, placeOf("rev-plain")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(standin.requests())
			params := map[string]string{"docId": standin.doc(tc.doc)}
			for k, v := range tc.params {
				params[k] = v
			}
			_, err := source.BuildBody(context.Background(), docs, "table", params, "tok", home, store.ReadSecretFile)
			var refused *source.CallError
			if !errors.As(err, &refused) || refused.Reason != source.BuildRefused || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("err = %v, want %s containing %q", err, source.BuildRefused, tc.words)
			}
			past := standin.requests()[before:]
			if len(past) != tc.heard {
				t.Fatalf("the stand-in heard %d requests past the %d before, want %d", len(past), before, tc.heard)
			}
			for i, r := range past {
				want := "GET"
				if tc.place != "" && i == 1 {
					want = "POST"
				}
				if r.method != want {
					t.Fatalf("request %d past the %d before is %s %s, want %s", i, before, r.method, r.path, want)
				}
			}
			if tc.place == "" {
				return
			}
			post := past[1]
			if post.path != "/v1/documents/"+standin.doc(tc.doc)+":batchUpdate" {
				t.Fatalf("place = %s %s", post.method, post.path)
			}
			if post.auth != "Bearer tok" {
				t.Fatalf("place Authorization = %q, want the call's credential", post.auth)
			}
			var got, want any
			if err := json.Unmarshal(post.body, &got); err != nil {
				t.Fatalf("placed body = %s: %v", post.body, err)
			}
			if err := json.Unmarshal([]byte(tc.place), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("placed body = %s, want %s", post.body, tc.place)
			}
		})
	}
}

// The fill posted: a table sends its place itself and the yard sends the
// fill, each answered with one reply per request. The stand-in hears the
// pre-read, the place, the second read and the fill. It answers 400 to a
// fill whose borders carry a width alone, which is the body the table call
// first printed for borders=false and Google refused: the borderless row,
// whose fill it accepts, and the last row, whose fill it refuses, are the
// two halves of one proof that the script's body is the whole border.
func TestGoogleDocsTableRoundTrip(t *testing.T) {
	widthAlone := `{"width":{"magnitude":0,"unit":"PT"}}`
	oldFill := `{"requests":[{"updateTableCellStyle":{"tableStartLocation":{"index":8,"tabId":"t.0"},` +
		`"tableCellStyle":{"borderTop":` + widthAlone + `,"borderBottom":` + widthAlone +
		`,"borderLeft":` + widthAlone + `,"borderRight":` + widthAlone + `},` +
		`"fields":"borderTop,borderBottom,borderLeft,borderRight"}},` +
		`{"insertText":{"location":{"index":13,"tabId":"t.0"},"text":"CHF"}},` +
		`{"insertText":{"location":{"index":11,"tabId":"t.0"},"text":"Item"}}],` +
		`"writeControl":{"requiredRevisionId":"rev-one-placed"}}`
	for _, tc := range []struct {
		name    string
		cells   string
		borders string // "" leaves borders out
		sent    string // posted in place of the fill built; "" posts the built one
		replies string // "" where the stand-in refuses the fill
	}{
		{"with borders", `[["Item","CHF"],["Bread","1.20"]]`, "", "", `[{},{},{},{}]`},
		{"without borders", `[["Item","CHF"]]`, "false", "", `[{},{},{}]`},
		{"borders with a width alone are refused", `[["Item","CHF"]]`, "false", oldFill, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			home := layoutHome(t)
			params := map[string]string{"docId": standin.doc("one"), "find": "{{logo}}", "cells": tc.cells}
			if tc.borders != "" {
				params["borders"] = tc.borders
			}
			body, err := source.BuildBody(context.Background(), docs, "table", params, "tok", home, store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			fill := body
			if tc.sent != "" {
				fill = json.RawMessage(tc.sent)
			}
			res, err := source.DoKeyedBody(context.Background(), docs, "table", params, "tok", "", fill, store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			gotReqs := standin.requests()
			if len(gotReqs) != 4 {
				t.Fatalf("the stand-in heard %d requests, want the pre-read, the place, the second read and the fill", len(gotReqs))
			}
			for i, want := range []string{"GET", "POST", "GET", "POST"} {
				if gotReqs[i].method != want {
					t.Fatalf("request %d is %s %s, want %s", i, gotReqs[i].method, gotReqs[i].path, want)
				}
			}
			post := gotReqs[3]
			if post.path != "/v1/documents/"+standin.doc("one")+":batchUpdate" {
				t.Fatalf("fill = %s %s", post.method, post.path)
			}
			if post.auth != "Bearer tok" {
				t.Fatalf("fill Authorization = %q", post.auth)
			}
			var got, want any
			if err := json.Unmarshal(post.body, &got); err != nil {
				t.Fatalf("posted body = %s: %v", post.body, err)
			}
			if err := json.Unmarshal(fill, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("posted body = %s, want %s", post.body, fill)
			}
			if tc.replies == "" {
				// The refusal is a Result with the status and no answer: what
				// a service says of a request it refused is not kept.
				if res.Status != 400 || len(res.Answer) != 0 {
					t.Fatalf("the fill was answered %d %s, want 400 and no answer", res.Status, res.Answer)
				}
				return
			}
			if want := `{"documentId":"` + standin.doc("one") + `","replies":` + tc.replies + `}`; string(res.Answer) != want {
				t.Fatalf("answer = %s, want %s", res.Answer, want)
			}
		})
	}
}

// The style call builds its batchUpdate per call: its build script reads
// the document, finds the one match of its text, in a table cell too, and
// prints the one batchUpdate styling it, pinned to the revision read: an
// updateTextStyle over the match for bold, size and color, and an
// updateParagraphStyle over the same range for align, which styles the
// paragraph the match stands in. Every index below is counted by hand
// from the document the stand-in serves, not worked out the way the
// script does. No test calls Google.
func TestGoogleDocsStyleBuilds(t *testing.T) {
	grey := `{"color":{"rgbColor":{"red":0.5019607843137255,"green":0.5019607843137255,"blue":0.5019607843137255}}}`
	for _, tc := range []struct {
		name  string
		doc   string // the stand-in's doc
		find  string
		bold  string // "" leaves the property out
		size  string
		color string
		align string
		tab   string // the tab the match stands in
		start int    // the match's range
		end   int
		text  string // the textStyle built, "" for no updateTextStyle
		mask  string // its fields
		para  string // the alignment built, "" for no updateParagraphStyle
	}{
		{name: "bold in a paragraph", doc: "one", find: "Lunch", bold: "true",
			tab: "t.0", start: 1, end: 6, text: `{"bold":true}`, mask: "bold"},
		{name: "bold removed from a paragraph", doc: "one", find: "Lunch", bold: "false",
			tab: "t.0", start: 1, end: 6, text: `{"bold":false}`, mask: "bold"},
		{name: "every property at once", doc: "one", find: "{{logo}}", bold: "true", size: "12", color: "808080", align: "center",
			tab: "t.0", start: 7, end: 15,
			text: `{"bold":true,"fontSize":{"magnitude":12,"unit":"PT"},"foregroundColor":` + grey + `}`,
			mask: "bold,fontSize,foregroundColor", para: "CENTER"},
		{name: "right-aligned in a cell", doc: "cell", find: "{{logo}}", align: "right",
			tab: "t.0", start: 10, end: 18, para: "END"},
		{name: "a smaller grey match", doc: "one", find: "places", size: "9", color: "808080",
			tab: "t.0", start: 16, end: 22,
			text: `{"fontSize":{"magnitude":9,"unit":"PT"},"foregroundColor":` + grey + `}`,
			mask: "fontSize,foregroundColor"},
		{name: "an uppercase colour is read", doc: "one", find: "Lunch", color: "FF0000",
			tab: "t.0", start: 1, end: 6,
			text: `{"foregroundColor":{"color":{"rgbColor":{"red":1,"green":0,"blue":0}}}}`,
			mask: "foregroundColor"},
		{name: "black and left", doc: "one", find: "Lunch", color: "000000", align: "left",
			tab: "t.0", start: 1, end: 6,
			text: `{"foregroundColor":{"color":{"rgbColor":{"red":0,"green":0,"blue":0}}}}`,
			mask: "foregroundColor", para: "START"},
		{name: "a match in a child tab", doc: "second", find: "{{logo}}", bold: "true",
			tab: "t.1.1", start: 1, end: 9, text: `{"bold":true}`, mask: "bold"},
		{name: "an emoji match spans two units", doc: "emoji", find: "🍜", size: "14",
			tab: "t.0", start: 7, end: 9,
			text: `{"fontSize":{"magnitude":14,"unit":"PT"}}`, mask: "fontSize"},
		{name: "a match after an inline picture", doc: "gap", find: "go}}", bold: "true",
			tab: "t.0", start: 6, end: 10, text: `{"bold":true}`, mask: "bold"},
		{name: "bold false and size", doc: "one", find: "Lunch", bold: "false", size: "11",
			tab: "t.0", start: 1, end: 6,
			text: `{"bold":false,"fontSize":{"magnitude":11,"unit":"PT"}}`, mask: "bold,fontSize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			params := map[string]string{"docId": standin.doc(tc.doc), "find": tc.find}
			for k, v := range map[string]string{"bold": tc.bold, "size": tc.size, "color": tc.color, "align": tc.align} {
				if v != "" {
					params[k] = v
				}
			}
			body, err := source.BuildBody(context.Background(), docs, "style", params, "tok", layoutHome(t), store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			revision := map[string]string{
				"one": "rev-one", "cell": "rev-cell", "second": "rev-second",
				"emoji": "rev-emoji", "gap": "rev-gap",
			}[tc.doc]
			rng := `"startIndex":` + strconv.Itoa(tc.start) + `,"endIndex":` + strconv.Itoa(tc.end) + `,"tabId":` + strconv.Quote(tc.tab)
			var reqs []string
			if tc.text != "" {
				reqs = append(reqs, `{"updateTextStyle":{"range":{`+rng+`},"textStyle":`+tc.text+`,"fields":`+strconv.Quote(tc.mask)+`}}`)
			}
			if tc.para != "" {
				reqs = append(reqs, `{"updateParagraphStyle":{"range":{`+rng+`},"paragraphStyle":{"alignment":`+strconv.Quote(tc.para)+`},"fields":"alignment"}}`)
			}
			golden := `{"requests":[` + strings.Join(reqs, ",") + `],"writeControl":{"requiredRevisionId":` + strconv.Quote(revision) + `}}`
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("built body = %s: %v", body, err)
			}
			if err := json.Unmarshal([]byte(golden), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("built body = %s, want %s", body, golden)
			}
			// The pre-read the match came from: the doc, read with the
			// call's credential, as the replace call reads it.
			gotReqs := standin.requests()
			if len(gotReqs) != 1 {
				t.Fatalf("the stand-in heard %d requests, want the one pre-read", len(gotReqs))
			}
			pre := gotReqs[0]
			if pre.method != "GET" || pre.path != "/v1/documents/"+standin.doc(tc.doc) {
				t.Fatalf("pre-read = %s %s", pre.method, pre.path)
			}
			if pre.auth != "Bearer tok" {
				t.Fatalf("pre-read Authorization = %q, want the call's credential", pre.auth)
			}
			if pre.query.Get("fields") != "revisionId,tabs" || pre.query.Get("includeTabsContent") != "true" {
				t.Fatalf("pre-read query = %v", pre.query)
			}
		})
	}
}

// What the style call refuses to build: nothing is posted, and the caller
// reads the script's own words. The stand-in hears the pre-read where the
// refusal needed the document, and never a batchUpdate; where the refusal
// is of the parameters alone, it hears nothing at all.
func TestGoogleDocsStyleRefusals(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	for _, tc := range []struct {
		name   string
		doc    string
		params map[string]string
		words  string
		pre    bool // the refusal came after the pre-read
	}{
		{"no property", "one", map[string]string{"find": "Lunch"}, "style names no property to change: one of bold, size, color, align", false},
		{"a size of none", "one", map[string]string{"find": "Lunch", "size": "0"}, "size is a whole number of points above 0", false},
		{"a negative size", "one", map[string]string{"find": "Lunch", "size": "-2"}, "size is a whole number of points above 0", false},
		{"a colour of no hex", "one", map[string]string{"find": "Lunch", "color": "xyz"}, "color is six hex digits, such as 808080", false},
		{"a colour too short", "one", map[string]string{"find": "Lunch", "color": "FFF"}, "color is six hex digits, such as 808080", false},
		{"a colour of no hex digits", "one", map[string]string{"find": "Lunch", "color": "gggggg"}, "color is six hex digits, such as 808080", false},
		{"an empty colour", "one", map[string]string{"find": "Lunch", "color": ""}, "color is six hex digits, such as 808080", false},
		{"an empty find", "one", map[string]string{"find": "", "bold": "true"}, "find names no text to style", false},
		{"a find with a line break", "one", map[string]string{"find": "Lunch\nFriday", "bold": "true"}, "find holds a line break: the match stands in one paragraph", false},
		{"an unknown match", "one", map[string]string{"find": "{{nope}}", "bold": "true"}, "no match: find is in no text of the body", true},
		{"a match of another case", "one", map[string]string{"find": "{{LOGO}}", "bold": "true"}, "no match: find is in no text of the body", true},
		{"a match only in a header", "headed", map[string]string{"find": "{{logo}}", "bold": "true"}, "no match: find is in no text of the body", true},
		{"a match cut by an inline picture", "gap", map[string]string{"find": "{{logo}}", "bold": "true"}, "no match: find is in no text of the body", true},
		{"a second match", "twice", map[string]string{"find": "{{logo}}", "bold": "true"}, "2 matches of find in the body: style one match at a time, so make find match exactly one", true},
		{"a second match in a table cell", "tabled", map[string]string{"find": "{{logo}}", "bold": "true"}, "2 matches of find in the body: style one match at a time, so make find match exactly one", true},
		{"no such doc", "missing", map[string]string{"find": "{{logo}}", "bold": "true"}, "could not be read", true},
		{"no revision id", "no-revision", map[string]string{"find": "Lunch", "bold": "true"}, "answered no revision id", true},
		{"no tabs", "no-tabs", map[string]string{"find": "Lunch", "bold": "true"}, "answered no tabs", true},
		{"runs that overlap", "overlap", map[string]string{"find": "{{logo}}", "bold": "true"}, "answered text runs whose indices overlap", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(standin.requests())
			params := map[string]string{"docId": standin.doc(tc.doc)}
			for k, v := range tc.params {
				params[k] = v
			}
			_, err := source.BuildBody(context.Background(), docs, "style", params, "tok", home, store.ReadSecretFile)
			var refused *source.CallError
			if !errors.As(err, &refused) || refused.Reason != source.BuildRefused || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("err = %v, want %s containing %q", err, source.BuildRefused, tc.words)
			}
			want := 0
			if tc.pre {
				want = 1
			}
			past := standin.requests()[before:]
			if len(past) != want {
				t.Fatalf("the stand-in heard %d requests past the %d before, want %d", len(past), before, want)
			}
			for _, r := range past {
				if r.method == "POST" {
					t.Fatalf("the stand-in was sent %s %s: a refusal sends nothing", r.method, r.path)
				}
			}
		})
	}
}

// The built body posted: a style sends its requests in one batchUpdate and
// the answer carries one reply per request. The stand-in hears the pre-read
// and the post.
func TestGoogleDocsStyleRoundTrip(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	params := map[string]string{"docId": standin.doc("one"), "find": "Lunch", "bold": "true", "align": "left"}
	body, err := source.BuildBody(context.Background(), docs, "style", params, "tok", home, store.ReadSecretFile)
	if err != nil {
		t.Fatal(err)
	}
	res, err := source.DoKeyedBody(context.Background(), docs, "style", params, "tok", "", body, store.ReadSecretFile)
	if err != nil {
		t.Fatal(err)
	}
	gotReqs := standin.requests()
	if len(gotReqs) != 2 {
		t.Fatalf("the stand-in heard %d requests, want the pre-read and the post", len(gotReqs))
	}
	post := gotReqs[1]
	if post.method != "POST" || post.path != "/v1/documents/"+standin.doc("one")+":batchUpdate" {
		t.Fatalf("post = %s %s", post.method, post.path)
	}
	if post.auth != "Bearer tok" {
		t.Fatalf("post Authorization = %q", post.auth)
	}
	var got, want any
	if err := json.Unmarshal(post.body, &got); err != nil {
		t.Fatalf("posted body = %s: %v", post.body, err)
	}
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("posted body = %s, want the built %s", post.body, body)
	}
	if want := `{"documentId":"` + standin.doc("one") + `","replies":[{},{}]}`; string(res.Answer) != want {
		t.Fatalf("answer = %s, want %s", res.Answer, want)
	}
}

// The edit call builds its batchUpdate per call without reading
// anything: its build script sends the caller's requests as it wrote
// them, with no allow-list, pinned to the revision it was given. A
// delete and a document-wide setting pass unchanged, as does an opaque
// revision the yard checks no pattern of. The stand-in hears nothing of
// any build below. No test calls Google.
func TestGoogleDocsEditBuilds(t *testing.T) {
	fifty := `[` + strings.TrimSuffix(strings.Repeat(`{"insertText":{"location":{"index":1},"text":"x"}},`, 50), ",") + `]`
	for _, tc := range []struct {
		name     string
		revision string
		requests string
	}{
		{name: "one request", revision: "rev-one",
			requests: `[{"updateTextStyle":{"range":{"startIndex":1,"endIndex":6},"textStyle":{"bold":true},"fields":"bold"}}]`},
		{name: "several requests", revision: "rev-9x",
			requests: `[{"insertText":{"location":{"index":7},"text":"Friday"}},{"updateTextStyle":{"range":{"startIndex":7,"endIndex":13},"textStyle":{"bold":true},"fields":"bold"}}]`},
		{name: "a delete passes unchanged", revision: "rev-one",
			requests: `[{"deleteContentRange":{"range":{"startIndex":7,"endIndex":15,"tabId":"t.0"}}}]`},
		{name: "a document-wide setting passes unchanged", revision: "rev-one",
			requests: `[{"updateDocumentStyle":{"documentStyle":{"marginTop":{"magnitude":72,"unit":"PT"}},"fields":"marginTop"}}]`},
		{name: "fifty requests pass", revision: "rev-fifty", requests: fifty},
		{name: "an opaque revision passes through quoted", revision: `r"ev\9`,
			requests: `[{"updateTextStyle":{"range":{"startIndex":1,"endIndex":6},"textStyle":{"bold":true},"fields":"bold"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			standin, srv := imageStandin(t)
			defer srv.Close()
			docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
			body, err := source.BuildBody(context.Background(), docs, "edit",
				map[string]string{"docId": standin.doc("one"), "revision": tc.revision, "requests": tc.requests},
				"tok", layoutHome(t), store.ReadSecretFile)
			if err != nil {
				t.Fatal(err)
			}
			golden := `{"requests":` + tc.requests + `,"writeControl":{"requiredRevisionId":` + strconv.Quote(tc.revision) + `}}`
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("built body = %s: %v", body, err)
			}
			if err := json.Unmarshal([]byte(golden), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("built body = %s, want %s", body, golden)
			}
			if n := len(standin.requests()); n != 0 {
				t.Fatalf("the stand-in heard %d requests, want none: the edit reads nothing", n)
			}
		})
	}
}

// What the edit call refuses to build: nothing is printed for the yard to
// send, and the caller reads the script's own words. The stand-in hears
// nothing of any refusal, since no refusal needs the document.
func TestGoogleDocsEditRefusals(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	many := `[` + strings.TrimSuffix(strings.Repeat(`{"a":1},`, 51), ",") + `]`
	noArray := "requests is no JSON array of Docs batchUpdate requests"
	noRequest := "is no Docs request: one object with exactly one key"
	for _, tc := range []struct {
		name     string
		revision string // "" hands an empty revision; "-" leaves it out for the core to refuse
		requests string // "-" leaves requests out for the core to refuse
		words    string
		reason   string
	}{
		{"requests are no JSON", "r", "nope", noArray, source.BuildRefused},
		{"requests are no array", "r", "3", noArray, source.BuildRefused},
		{"requests are null", "r", "null", noArray, source.BuildRefused},
		{"requests are empty", "r", "", noArray, source.BuildRefused},
		{"requests hold no request", "r", "[]", "requests holds no request to send", source.BuildRefused},
		{"requests hold fifty-one", "r", many, "requests holds 51 requests, and an edit sends at most 50", source.BuildRefused},
		{"a request that is a number", "r", "[3]", "request 1 " + noRequest, source.BuildRefused},
		{"a request that is null", "r", "[null]", "request 1 " + noRequest, source.BuildRefused},
		{"a request that is an array", "r", `[[{"a":1}]]`, "request 1 " + noRequest, source.BuildRefused},
		{"a request with two keys", "r", `[{"a":1,"b":2}]`, "request 1 " + noRequest, source.BuildRefused},
		{"a request with no key", "r", `[{}]`, "request 1 " + noRequest, source.BuildRefused},
		{"the second request bad", "r", `[{"a":1},"x"]`, "request 2 " + noRequest, source.BuildRefused},
		{"the third request bad", "r", `[{"a":1},{"b":2},7]`, "request 3 " + noRequest, source.BuildRefused},
		{"an empty revision", "", `[{"a":1}]`, "edit was handed no revision", source.BuildRefused},
		{"no revision", "-", `[{"a":1}]`, "parameter revision is missing", source.BadParam},
		{"no requests", "r", "-", "parameter requests is missing", source.BadParam},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]string{"docId": standin.doc("one")}
			if tc.revision != "-" {
				params["revision"] = tc.revision
			}
			if tc.requests != "-" {
				params["requests"] = tc.requests
			}
			_, err := source.BuildBody(context.Background(), docs, "edit", params, "tok", home, store.ReadSecretFile)
			var refused *source.CallError
			if !errors.As(err, &refused) || refused.Reason != tc.reason || !strings.Contains(err.Error(), tc.words) {
				t.Fatalf("err = %v, want %s containing %q", err, tc.reason, tc.words)
			}
			if n := len(standin.requests()); n != 0 {
				t.Fatalf("the stand-in heard %d requests, want none: a refusal sends nothing", n)
			}
		})
	}
}

// The built body posted: an edit sends its requests in one batchUpdate
// and the answer carries one reply per request. The stand-in hears the
// post alone, with no pre-read before it.
func TestGoogleDocsEditRoundTrip(t *testing.T) {
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	params := map[string]string{"docId": standin.doc("one"), "revision": "rev-one",
		"requests": `[{"updateTextStyle":{"range":{"startIndex":1,"endIndex":6},"textStyle":{"bold":true},"fields":"bold"}},{"deleteContentRange":{"range":{"startIndex":7,"endIndex":15}}}]`}
	body, err := source.BuildBody(context.Background(), docs, "edit", params, "tok", home, store.ReadSecretFile)
	if err != nil {
		t.Fatal(err)
	}
	res, err := source.DoKeyedBody(context.Background(), docs, "edit", params, "tok", "", body, store.ReadSecretFile)
	if err != nil {
		t.Fatal(err)
	}
	gotReqs := standin.requests()
	if len(gotReqs) != 1 {
		t.Fatalf("the stand-in heard %d requests, want the post alone", len(gotReqs))
	}
	post := gotReqs[0]
	if post.method != "POST" || post.path != "/v1/documents/"+standin.doc("one")+":batchUpdate" {
		t.Fatalf("post = %s %s", post.method, post.path)
	}
	if post.auth != "Bearer tok" {
		t.Fatalf("post Authorization = %q", post.auth)
	}
	var got, want any
	if err := json.Unmarshal(post.body, &got); err != nil {
		t.Fatalf("posted body = %s: %v", post.body, err)
	}
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("posted body = %s, want the built %s", post.body, body)
	}
	if want := `{"documentId":"` + standin.doc("one") + `","replies":[{},{}]}`; string(res.Answer) != want {
		t.Fatalf("answer = %s, want %s", res.Answer, want)
	}
}

// No argument of a process any of the three scripts starts holds the
// credential, the cells' texts, the match, or the edit's revision and
// requests: an argument is readable by every process on the machine while
// it runs, so the scripts hand curl its header as a config line on a pipe
// and its batchUpdate body on another, and jq the parameters and the
// document on its stdin. A stand-in curl and a stand-in jq first on PATH
// record their arguments and delegate to the real ones.
func TestGoogleDocsLayoutHandsNoSecretToAnyArgument(t *testing.T) {
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	for _, tool := range []string{"curl", "jq"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " is not installed, and the table, style and edit build scripts run it")
		}
		scripttest.Write(t, filepath.Join(bin, tool),
			"printf '%s\n' \"$@\" >> \""+argv+"\"\nexec \""+real+"\" \"$@\"\n")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	standin, srv := imageStandin(t)
	defer srv.Close()
	docs := googleDeclaration(t, "google-docs.yaml", "https://docs.googleapis.com", srv.URL)
	home := layoutHome(t)
	if _, err := source.BuildBody(context.Background(), docs, "table",
		map[string]string{"docId": standin.doc("one"), "find": "{{logo}}", "cells": `[["Item","s3cr3t-price"]]`},
		"the-secret-token", home, store.ReadSecretFile); err != nil {
		t.Fatal(err)
	}
	// The style call reads another doc: past the table's place, the
	// first reads as the placed table, with no match left in it.
	if _, err := source.BuildBody(context.Background(), docs, "style",
		map[string]string{"docId": standin.doc("split"), "find": "{{logo}}", "bold": "true"},
		"the-secret-token", home, store.ReadSecretFile); err != nil {
		t.Fatal(err)
	}
	// The edit call reads no doc at all: its revision and requests reach
	// jq on its stdin, never as an argument.
	if _, err := source.BuildBody(context.Background(), docs, "edit",
		map[string]string{"docId": standin.doc("one"), "revision": "rev-s3cr3t",
			"requests": `[{"insertText":{"location":{"index":7},"text":"s3cr3t-text"}}]`},
		"the-secret-token", home, store.ReadSecretFile); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"the-secret-token", "s3cr3t-price", "{{logo}}", "rev-s3cr3t", "s3cr3t-text"} {
		if strings.Contains(string(recorded), secret) {
			t.Fatalf("a process was called with %q in an argument: %s", secret, recorded)
		}
	}
	// The reads and the place took the credential, out of the arguments;
	// the edit sent nothing.
	reqs := standin.requests()
	if len(reqs) != 4 {
		t.Fatalf("the stand-in heard %d requests, want the table's three and the style's read", len(reqs))
	}
	for _, r := range reqs {
		if r.auth != "Bearer the-secret-token" {
			t.Fatalf("%s %s Authorization = %q, want the call's credential", r.method, r.path, r.auth)
		}
	}
}

// layoutHome installs the table, style and edit calls' build scripts from
// the embedded pack, as a source add copies them, and returns the home
// they stand under. The table and style scripts read docs with curl and
// lay their text out with jq; the edit script needs jq alone.
func layoutHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed, and the table and style build scripts read the doc with it")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed, and the table, style and edit build scripts shape their bodies with it")
	}
	var copies []source.FileCopy
	for _, prog := range []string{"google-docs/build-table", "google-docs/build-style", "google-docs/build-edit"} {
		script, err := fs.ReadFile(packs.FS, "google-docs/"+prog)
		if err != nil {
			t.Fatal(err)
		}
		copies = append(copies, source.FileCopy{Program: prog, Data: script})
	}
	home := t.TempDir()
	if err := source.WriteCopies(home, "google-docs", copies); err != nil {
		t.Fatal(err)
	}
	scripttest.Relink(t, filepath.Join(home, "sources", "google-docs"))
	return home
}
