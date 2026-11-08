package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"yardr.dev/yardr/internal/store"
)

type unreadOnlyYard struct{ Yard }

func (unreadOnlyYard) UserInbox(context.Context, string, int, string) (*store.Mailbox, error) {
	return &store.Mailbox{Unread: 1}, nil
}

func TestWaitForUsesTheMeasuredClientLimit(t *testing.T) {
	for _, tc := range []struct {
		client string
		want   time.Duration
	}{
		{"Claude Code", DefaultWait},
		{"claude-code", DefaultWait},
		{"Claude Desktop", ShortWait},
		{"codex", ShortWait},
		{"", ShortWait},
	} {
		t.Run(tc.client, func(t *testing.T) {
			if got := waitFor(tc.client); got != tc.want {
				t.Fatalf("waitFor(%q) = %s, want %s", tc.client, got, tc.want)
			}
		})
	}
	// Claude Code moves a call still open at 120 s to the background, so
	// the wait for it ends well inside that, with room for the trip back.
	if limit := 120 * time.Second; DefaultWait > limit-10*time.Second {
		t.Errorf("DefaultWait = %s, want at least 10 s under Claude Code's %s tool limit", DefaultWait, limit)
	}
}

func TestToolArgumentNumbersRequireWholePositiveNumbers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"fractional question", map[string]any{"work": "yd-1", "question": json.Number("7.9")}},
		{"zero question", map[string]any{"work": "yd-1", "question": json.Number("0")}},
		{"fractional choice", map[string]any{"work": "yd-1", "question": json.Number("7"), "pick": json.Number("1.9")}},
		{"zero choice", map[string]any{"work": "yd-1", "question": json.Number("7"), "pick": json.Number("0")}},
		{"negative choice", map[string]any{"work": "yd-1", "question": json.Number("7"), "pick": json.Number("-1")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := answerInput(tc.args); err == nil {
				t.Fatal("accepted an inexact or nonpositive number")
			}
		})
	}
	if _, err := answerInput(map[string]any{
		"work": "yd-1", "question": json.Number("7"), "pick": json.Number("2"),
	}); err != nil {
		t.Fatalf("a whole positive question and choice: %v", err)
	}

	if n, err := recentArg(map[string]any{}); err != nil || n != 0 {
		t.Fatalf("a missing recent = %d, %v; want the plain call", n, err)
	}
	if n, err := recentArg(map[string]any{"recent": nil}); err != nil || n != 0 {
		t.Fatalf("a null recent = %d, %v; want the plain call, as a null wait is the default", n, err)
	}
	if n, err := recentArg(map[string]any{"recent": json.Number("3")}); err != nil || n != 3 {
		t.Fatalf("recent 3 = %d, %v", n, err)
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"zero", json.Number("0")},
		{"negative", json.Number("-2")},
		{"fractional", json.Number("2.5")},
		{"text", "two"},
		{"boolean", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := recentArg(map[string]any{"recent": tc.value}); err == nil {
				t.Fatalf("recent %v was served", tc.value)
			}
		})
	}
}

func TestWeighedWordDoesNotCoerceNumericContent(t *testing.T) {
	req := &sdkmcp.CallToolRequest{Params: &sdkmcp.CallToolParamsRaw{
		InputResponses: sdkmcp.InputResponseMap{
			"weighed": &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"number": 7.9}},
		},
	}}
	weighed, refusal, err := weighedWord(req, 7)
	if err != nil || weighed || refusal == "" {
		t.Fatalf("fractional typed-back content = %v, %q, %v; want a refusal", weighed, refusal, err)
	}
	req.Params.InputResponses["weighed"] = &sdkmcp.ElicitResult{
		Action: "accept", Content: map[string]any{"number": "7"},
	}
	weighed, refusal, err = weighedWord(req, 7)
	if err != nil || !weighed || refusal != "" {
		t.Fatalf("matching typed-back content = %v, %q, %v; want acceptance", weighed, refusal, err)
	}
}

func TestMalformedArgumentsKeepUnreadFooter(t *testing.T) {
	server := &Server{Service: &Service{Yard: unreadOnlyYard{}, User: "ada"}}
	for _, raw := range []string{"{", "null", "{} {}"} {
		t.Run(raw, func(t *testing.T) {
			result, err := server.call(context.Background(), &sdkmcp.CallToolRequest{
				Params: &sdkmcp.CallToolParamsRaw{Arguments: json.RawMessage(raw)},
			}, ToolReadMyMail)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || len(result.Content) != 1 {
				t.Fatalf("malformed call = %+v, want one error result", result)
			}
			text := result.Content[0].(*sdkmcp.TextContent).Text
			if !strings.Contains(text, "the arguments are not a JSON object") || !strings.HasSuffix(text, Footer(1)) {
				t.Fatalf("malformed call text = %q, want refusal and unread footer", text)
			}
		})
	}
}
