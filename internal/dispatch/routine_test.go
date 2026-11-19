package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// routineStandin takes the routine's POSTs and keeps their bodies.
type routineStandin struct {
	*httptest.Server
	status atomic.Int64
	mu     sync.Mutex
	got    []map[string]any
}

func newRoutineStandin(t *testing.T) *routineStandin {
	t.Helper()
	s := &routineStandin{}
	s.status.Store(200)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("the routine body is no JSON: %s", raw)
		}
		s.mu.Lock()
		s.got = append(s.got, body)
		s.mu.Unlock()
		w.WriteHeader(int(s.status.Load()))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *routineStandin) posts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any{}, s.got...)
}

// withRoutine gives ada the routine beside her phone: the same env file
// holds her phone address and her routine URL, at a stand-in with a
// secret path. Ada is made an operator, whose URL on this machine the
// guard takes as any address of theirs; a member's is held to a public
// name (notify's test). The URL joins the yard's secrets.
func (y *pushYard) withRoutine(t *testing.T, s *routineStandin) string {
	t.Helper()
	ctx := context.Background()
	url := s.URL + "/hook/routine-s3cret-for-ada"
	must(t, os.WriteFile(y.env, []byte("PUSH_USER="+pushAddress+"\nROUTINE_URL="+url+"\n"), 0o600))
	_, err := y.api.SetUser(ctx, "ada", map[string]string{store.UserRole: store.RoleOperator}, "jo")
	must(t, err)
	_, err = y.api.SetUser(ctx, "ada", map[string]string{store.UserWebhook: "routine"}, "ada")
	must(t, err)
	y.secrets = append(y.secrets, url, "routine-s3cret-for-ada")
	return url
}

// The routine gets the phone's message once, first, with what the batch
// is about beside the words; the phone is told as before. Words alone
// (no webhook) and items-free (declared) stay byte-identical: the phone
// stand-in's body above has no items in it.
func TestRoutineBesideThePhone(t *testing.T) {
	t.Parallel()
	y := newPushYard(t)
	r := newRoutineStandin(t)
	ctx := context.Background()
	y.withRoutine(t, r)
	y.mail(t, "yardmaster", "the tests are green")
	seq := y.queued(t)[0].Seq
	y.settle(t)
	want(t, "the phone", y.sent(), []string{"mail from yardmaster | the tests are green"})
	posts := r.posts()
	if len(posts) != 1 || posts[0]["title"] != "mail from yardmaster" || posts[0]["text"] != "the tests are green" {
		t.Fatalf("the routine got %v", posts)
	}
	items, _ := posts[0]["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["kind"] != "mail" || items[0].(map[string]any)["seq"] != float64(seq) {
		t.Fatalf("the routine got items %v", posts[0]["items"])
	}
	u, err := y.api.GetUser(context.Background(), "ada")
	must(t, err)
	if u.LastWebhook == nil || u.LastWebhook.Status != 200 || u.LastWebhook.Source != "routine" || u.LastWebhook.Count != 1 {
		t.Fatalf("the routine's record: %+v", u.LastWebhook)
	}
	want(t, "left in the queue", len(y.queued(t)), 0)

	// A subscription notification carries its own kind and numbers beside
	// the words too, including the bead and what happened to it.
	must(t, y.api.PutDepot(ctx, store.Depot{Name: "yard", Base: "main"}, "jo"))
	flow, err := store.ParseFlow(pushFlow)
	must(t, err)
	flow.Name = "yard"
	_, err = y.api.PutFlow(ctx, *flow, "jo")
	must(t, err)
	must(t, y.api.SetFlowPointer(ctx, "yard", "", "yard", "jo"))
	must(t, y.api.AddSubscription(ctx, "ada", store.Subscription{Source: store.SourceYardr, Scope: store.ScopeAll,
		Where: map[string]string{"stage": "decide"}}, "ada"))
	_, err = y.api.CreateBead(ctx, store.NewBead{ID: "yard-1", Title: "Which way round", Depot: "yard", Stage: "new"}, "jo")
	must(t, err)
	_, err = y.api.Advance(ctx, "yard-1", "decide", store.Expect{}, "builder")
	must(t, err)
	queued := y.queued(t)
	if len(queued) != 1 || queued[0].Note == nil {
		t.Fatalf("the subscription queue = %+v", queued)
	}
	note := queued[0].Note
	noteSeq := queued[0].Seq
	y.settle(t)
	posts = r.posts()
	if len(posts) != 2 || posts[1]["text"] == "" {
		t.Fatalf("the routine's notification post = %v", posts)
	}
	items, _ = posts[1]["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the routine got notification items %v", posts[1]["items"])
	}
	item := items[0].(map[string]any)
	if item["kind"] != "notification" || item["seq"] != float64(noteSeq) || item["bead"] != note.Bead ||
		item["note"] != note.Kind || item["source"] != note.Source {
		t.Fatalf("the routine got notification item %v", item)
	}
}

// A routine that answers no success, or none, costs the phone nothing:
// the words go once, the failure is the routine's record and the log's,
// and nothing is retried for it.
func TestRoutineDownTheWordsGo(t *testing.T) {
	t.Parallel()
	y := newPushYard(t)
	r := newRoutineStandin(t)
	y.withRoutine(t, r)
	r.status.Store(500)
	y.mail(t, "yardmaster", "still told")
	y.settle(t)
	want(t, "the phone", y.sent(), []string{"mail from yardmaster | still told"})
	want(t, "the routine tried once", len(r.posts()), 1)
	u, err := y.api.GetUser(context.Background(), "ada")
	must(t, err)
	if u.LastWebhook == nil || u.LastWebhook.Status != 500 || u.LastWebhook.OK() {
		t.Fatalf("the routine's record: %+v", u.LastWebhook)
	}
	want(t, "left in the queue", len(y.queued(t)), 0)
	want(t, "push_failed events", y.failed(t), []string{})
	if !strings.Contains(y.log.String(), "user not told at their routine") {
		t.Fatalf("the log says nothing of the routine:\n%s", y.log.String())
	}
}

// Told at the routine alone, a user queues and wakes all the same, and
// the phone is sent nothing.
func TestWebhookOnlyWakes(t *testing.T) {
	t.Parallel()
	y := newPushYard(t)
	r := newRoutineStandin(t)
	y.withRoutine(t, r)
	ctx := context.Background()
	_, err := y.api.SetUser(ctx, "ada", map[string]string{store.UserNotify: ""}, "ada")
	must(t, err)
	y.mail(t, "yardmaster", "routine only")
	y.settle(t)
	want(t, "the phone", y.sent(), []string{})
	if posts := r.posts(); len(posts) != 1 || posts[0]["text"] != "routine only" {
		t.Fatalf("the routine got %v", posts)
	}
	want(t, "left in the queue", len(y.queued(t)), 0)
}

// A secret ask is told by its number alone on the phone and at the
// routine alike: no question, no asker, nothing of its context.
func TestRoutineSecretAskByNumber(t *testing.T) {
	t.Parallel()
	y := newPushYard(t)
	r := newRoutineStandin(t)
	y.withRoutine(t, r)
	ctx := context.Background()
	_, err := y.api.PutSource(ctx, "routine.yaml", "name: routine\nabout: A person's routine.\ncredential: { env: ROUTINE_URL, url: true }\n", "jo")
	must(t, err)
	b, err := y.api.CreateBead(ctx, store.NewBead{Title: "Give the routine its URL"}, "yardmaster")
	must(t, err)
	const question = "The routine URL only ada may read?"
	seq, err := y.api.AddAsk(ctx, b.ID, store.Ask{Question: question, To: "ada",
		Context: []string{"Context only ada may read."},
		Secret:  &store.AskSecret{Source: "routine", As: "ada"}}, "yardmaster")
	must(t, err)
	y.settle(t)
	want(t, "the phone", y.sent(), []string{"A question waits | Question " + fmt.Sprint(seq) + " waits for you."})
	posts := r.posts()
	if len(posts) != 1 || posts[0]["title"] != "A question waits" || posts[0]["text"] != "Question "+fmt.Sprint(seq)+" waits for you." {
		t.Fatalf("the routine got %v", posts)
	}
	for _, text := range []string{y.sent()[0], posts[0]["title"].(string) + posts[0]["text"].(string), y.log.String()} {
		if strings.Contains(text, question) || strings.Contains(text, "Context only ada") || strings.Contains(text, "yardmaster asks") {
			t.Fatalf("a secret ask leaked: %q", text)
		}
	}
	items, _ := posts[0]["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["kind"] != "ask" || items[0].(map[string]any)["ask"] != float64(seq) || items[0].(map[string]any)["bead"] != b.ID {
		t.Fatalf("the routine got items %v", posts[0]["items"])
	}
}
