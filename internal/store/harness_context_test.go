package store_test

import (
	"fmt"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

func TestContextSourceUsageIn(t *testing.T) {
	t.Parallel()
	muse := store.ContextSource{Type: "model_completed", TypeField: "payload.event.kind", Usage: "payload.event.usage", Tokens: []string{"input_tokens"}}
	codex := store.ContextSource{Type: "token_count", TypeField: "payload.type", Usage: "payload.info.last_token_usage", Tokens: []string{"input_tokens"}}
	agy := store.ContextSource{Type: "PLANNER_RESPONSE", Usage: ".", Tokens: []string{"input_tokens", "cache_read_tokens"}}
	for _, tc := range []struct {
		name   string
		source store.ContextSource
		line   string
		used   int
		ok     bool
	}{
		{"Muse input includes cache", muse, `{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":650000,"cache_read_tokens":600000,"cache_write_tokens":100,"cached_tokens":600000,"cache_read_input_tokens":600000,"cache_creation_input_tokens":100,"output_tokens":300,"reasoning_tokens":200}}}}`, 650000, true},
		{"another nested kind", muse, `{"payload":{"event":{"kind":"goal_usage_attribution","usage":{"input_tokens":650000}}}}`, 0, false},
		{"flat kind cannot match nested path", muse, `{"payload.event.kind":"model_completed","payload":{"event":{"usage":{"input_tokens":650000}}}}`, 0, false},
		{"no input yet", muse, `{"payload":{"event":{"kind":"model_completed","usage":{"output_tokens":30}}}}`, 0, false},
		{"zero input is no reading", muse, `{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":0}}}}`, 0, false},
		{"no usage object", muse, `{"payload":{"event":{"kind":"model_completed"}}}`, 0, false},
		{"Codex input includes cache, excludes running total", codex, `{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":89874,"cached_input_tokens":89472,"output_tokens":200},"total_token_usage":{"input_tokens":995515}}}}`, 89874, true},
		{"Codex no per-call input", codex, `{"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":995515}}}}`, 0, false},
		{"Codex another event", codex, `{"payload":{"type":"token_usage_record","info":{"last_token_usage":{"input_tokens":89874}}}}`, 0, false},
		{"default flat type and tokens", store.ContextSource{Type: "assistant", Usage: "message.usage"}, `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":5,"output_tokens":7}}}`, 115, true},
		{"all zero is no reading", store.ContextSource{Type: "assistant", Usage: "message.usage"}, `{"type":"assistant","message":{"usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}}}`, 0, false},
		{"partial zero still sums", store.ContextSource{Type: "assistant", Usage: "message.usage"}, `{"type":"assistant","message":{"usage":{"input_tokens":0,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}`, 100, true},
		{"pi partial zero still sums", store.ContextSource{Type: "assistant", TypeField: "message.role", Usage: "message.usage", Tokens: []string{"input", "cacheRead", "cacheWrite"}}, `{"type":"message","message":{"role":"assistant","usage":{"input":0,"cacheRead":150,"cacheWrite":0},"stopReason":"toolUse"}}`, 150, true},
		{"custom flat type", store.ContextSource{Type: "turn", TypeField: "kind", Usage: "use", Tokens: []string{"total_tokens"}}, `{"kind":"turn","use":{"total_tokens":9}}`, 9, true},
		{"agy top-level input and cache", agy, `{"type":"PLANNER_RESPONSE","created_at":"2026-11-24T10:41:53Z","model":"gemini-4-hydrogen-high","input_tokens":17939,"cache_read_tokens":0,"output_tokens":635}`, 17939, true},
		{"agy input excludes the cache", agy, `{"type":"PLANNER_RESPONSE","created_at":"2026-11-24T10:41:08Z","input_tokens":6526,"cache_read_tokens":508032,"output_tokens":6406}`, 514558, true},
		{"agy compaction summary is no current use", agy, `{"type":"CHECKPOINT","created_at":"2026-11-24T10:41:23Z","input_tokens":6526,"cache_read_tokens":508032,"output_tokens":6406}`, 0, false},
		{"agy prompt is no use", agy, `{"type":"USER_INPUT","created_at":"2026-11-24T10:41:08Z","content":"go on"}`, 0, false},
		{"agy tool output is no use", agy, `{"type":"GENERIC","created_at":"2026-11-24T10:41:08Z","content":"done"}`, 0, false},
		{"agy response without counts is no use", agy, `{"type":"PLANNER_RESPONSE","created_at":"2026-11-24T10:41:08Z"}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used, ok := tc.source.UsageIn([]byte(tc.line))
			if used != tc.used || ok != tc.ok {
				t.Fatalf("context = %d, %v; want %d, %v", used, ok, tc.used, tc.ok)
			}
		})
	}
	pi := store.ContextSource{Type: "assistant", TypeField: "message.role", Usage: "message.usage", Tokens: []string{"input", "cacheRead", "cacheWrite"}, Window: 200000}
	line := []byte(`{"type":"message","message":{"role":"assistant","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cacheWrite1h":0,"reasoning":0},"stopReason":"error"}}`)
	used, window, ok, reset, err := pi.ReadIn(line)
	if used != 0 || window != 0 || ok || reset || err != nil {
		t.Fatalf("pi ReadIn = %d, %d, %v, %v, %v; want 0, 0, false, false, nil", used, window, ok, reset, err)
	}
	t.Run("reported window", func(t *testing.T) {
		codex.WindowField = "payload.info.model_context_window"
		for _, tc := range []struct {
			value string
			want  int
			err   bool
		}{
			{"258400", 258400, false},
			{"475000", 475000, false},
			{"null", 0, false},
			{"0", 0, true},
			{"-1", 0, true},
			{"258400.5", 0, true},
			{`"private text"`, 0, true},
			{"{}", 0, true},
			{"[]", 0, true},
			{"1e30", 0, true},
		} {
			t.Run(tc.value, func(t *testing.T) {
				line := fmt.Sprintf(`{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":89874},"model_context_window":%s}}}`, tc.value)
				used, window, ok, reset, err := codex.ReadIn([]byte(line))
				if reset {
					t.Fatal("a Codex usage line is not a reset")
				}
				if tc.err {
					if err == nil || !strings.Contains(err.Error(), codex.WindowField) || strings.Contains(err.Error(), "private text") || used != 0 || window != 0 || ok {
						t.Fatalf("invalid window: %d, %d, %v, %v", used, window, ok, err)
					}
				} else if err != nil || !ok || used != 89874 || window != tc.want {
					t.Fatalf("context = %d, %d, %v, %v; want 89874, %d, true, nil", used, window, ok, err, tc.want)
				}
			})
		}
	})
	t.Run("agy model window and reset", func(t *testing.T) {
		agy.ModelField = "model"
		agy.ModelWindows = map[string]int{"gemini-4-neon-medium": 524288}
		agy.ResetType = "CHECKPOINT"
		for _, tc := range []struct {
			line         string
			used, window int
			ok, reset    bool
		}{
			{`{"type":"PLANNER_RESPONSE","model":"gemini-4-neon-medium","input_tokens":500,"cache_read_tokens":2000,"output_tokens":50}`, 2500, 524288, true, false},
			{`{"type":"PLANNER_RESPONSE","model":"unknown","input_tokens":100}`, 100, 0, true, false},
			{`{"type":"CHECKPOINT","input_tokens":500,"cache_read_tokens":2000}`, 0, 0, false, true},
			{`{"type":"GENERIC","input_tokens":500}`, 0, 0, false, false},
		} {
			used, window, ok, reset, err := agy.ReadIn([]byte(tc.line))
			if used != tc.used || window != tc.window || ok != tc.ok || reset != tc.reset || err != nil {
				t.Errorf("%s: context = %d, %d, %v, %v, %v; want %d, %d, %v, %v, nil",
					tc.line, used, window, ok, reset, err, tc.used, tc.window, tc.ok, tc.reset)
			}
		}
		used, window, ok, reset, err := agy.ReadIn([]byte(`{"type":"PLANNER_RESPONSE","model":{"private text":1},"input_tokens":100}`))
		if err == nil || !strings.Contains(err.Error(), "model") || strings.Contains(err.Error(), "private text") ||
			used != 0 || window != 0 || ok || reset {
			t.Errorf("malformed model returned %d, %d, %v, %v, %v", used, window, ok, reset, err)
		}
	})
}
