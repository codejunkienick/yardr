package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Routine tells a person's own routine: one POST of {title, text, items}
// to the URL in their env file, whose secret path is its only credential
// (an n8n, Make or Zapier webhook URL, or any program of theirs that
// takes one). The URL is the person's to give, by card as their own
// credential in the source routine (packs/routine), and stands in no
// error, event or log of the yard: every refusal below names none of it.
//
// Routine takes no deliveries: it is in Adapters so that NotifierFor
// finds it, and a hook of its name is refused by the pack's declaration
// having no hook key (AdapterFor), never by these methods answering one.
type Routine struct{}

func (Routine) Verify(_ *http.Request, _, _ []byte, _ time.Time) (string, error) {
	return "", unauthorized("routine takes no deliveries")
}

func (Routine) Bodies() BodyScope { return BodiesRepeat }

func (Routine) Normalize(_ http.Header, _ []byte) (*Envelope, error) {
	return nil, bad("routine takes no deliveries")
}

// NotifyEnv is the one name a user's env file sets for routine: the URL
// the yard POSTs each message to.
func (Routine) NotifyEnv() []string { return []string{"ROUTINE_URL"} }

// Notify POSTs the message to the URL env holds, as {title, text, items}
// with items an array even when the message is about nothing yet. The
// link a message carries goes nowhere: a routine opens what its items
// name. An answer, success or not, is the status; no answer is an error,
// and neither names the URL.
func (Routine) Notify(ctx context.Context, env map[string]string, title, text, _ string, items []Item) (int, error) {
	url := env["ROUTINE_URL"]
	if url == "" {
		return 0, fmt.Errorf("routine: no URL to tell")
	}
	if items == nil {
		items = []Item{}
	}
	body, err := json.Marshal(struct {
		Title string `json:"title"`
		Text  string `json:"text"`
		Items []Item `json:"items"`
	}{Title: title, Text: text, Items: items})
	if err != nil {
		return 0, fmt.Errorf("routine: the message is not sent: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("routine: the URL is no address to tell")
	}
	req.Header.Set("Content-Type", "application/json")
	client := *http.DefaultClient
	// The URL guard checks the address the person gave. Following a
	// redirect would send their message to an address it never checked.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("routine: the routine did not answer")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}
