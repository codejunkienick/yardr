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
		{"zero input is a reading", muse, `{"payload":{"event":{"kind":"model_completed","usage":{"input_tokens":0}}}}`, 0, true},
		{"Codex input includes cache, excludes running total", codex, `{"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":89874,"cached_input_tokens":89472,"output_tokens":200},"total_token_usage":{"input_tokens":995515}}}}`, 89874, true},
		{"Codex no per-call input", codex, `{"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":995515}}}}`, 0, false},
		{"Codex another event", codex, `{"payload":{"type":"token_usage_record","info":{"last_token_usage":{"input_tokens":89874}}}}`, 0, false},
		{"default flat type and tokens", store.ContextSource{Type: "assistant", Usage: "message.usage"}, `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":5,"output_tokens":7}}}`, 115, true},
		{"custom flat type", store.ContextSource{Type: "turn", TypeField: "kind", Usage: "use", Tokens: []string{"total_tokens"}}, `{"kind":"turn","use":{"total_tokens":9}}`, 9, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used, ok := tc.source.UsageIn([]byte(tc.line))
			if used != tc.used || ok != tc.ok {
				t.Fatalf("context = %d, %v; want %d, %v", used, ok, tc.used, tc.ok)
			}
		})
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
				used, window, ok, err := codex.ReadIn([]byte(line))
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
}
