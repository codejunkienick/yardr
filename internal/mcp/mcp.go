// Package mcp is the yard as an MCP server: what a person's own MCP
// client (Claude Code, Claude Desktop, claude.ai, or any other) is
// offered so it can work the yard as that person, in that person's
// words (design yardr-ts4d).
//
// The tool list, the schemas and the plain-words results are defined
// once, here, as a Go package with no CLI concerns in it. The local
// server (Server, served by yardr mcp over stdio) is one adapter over
// them; the edge's remote MCP endpoint is another, and checks its own
// list against Tools. Every tool call becomes store calls made as the
// one user the connection is for, and the store's policy decides each
// one, as it decides footplate's and chat's: this package is a window,
// not a new permission.
//
// Six of the design's seven tools live here, and two the design does
// not name: reword and withdraw, the person's own say over a request
// they filed. The seventh, add_a_note, is not served yet: it lands with
// the bead that carries the network filing promotions, and nothing here
// names it.
//
// No secret passes through MCP, on any path: a question that asks for
// one is refused by Service.Answer before any secret call could be
// made, in plain words naming where the secret is given instead. That
// gate lives here, in the server, so every adapter inherits it.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"yardr.dev/yardr/internal/store"
)

// The tools, in the person's words. No name uses a yard word, because
// the person who asked for this window knows none of them.
const (
	ToolWhatIsHappening  = "what_is_happening"
	ToolAskForSomething  = "ask_for_something"
	ToolWhatWaitsForMe   = "what_waits_for_me"
	ToolAnswer           = "answer"
	ToolAskTheYardmaster = "ask_the_yardmaster"
	ToolReadMyMail       = "read_my_mail"
	ToolReword           = "reword"
	ToolWithdraw         = "withdraw"
)

// FloorVersion is the oldest MCP revision this server speaks: every
// revision it serves (2025-06-18, 2025-11-25 and 2026-07-28) has
// elicitation, which a weighed answer needs, and an earlier client has
// no form to type a weighty answer back into (design yardr-ts4d 2.5).
const FloorVersion = "2025-06-18"

// FloorMessage is the one plain sentence naming that floor. Where the
// modern era lets the server answer a call with an error, the error
// carries it; on a legacy handshake no server message is promised (the
// SDK negotiates, and a client that cannot speak what comes back
// disconnects), so there it is the help text, not the handshake, that
// carries it.
const FloorMessage = "This yard speaks MCP 2025-06-18 or later. Update your client to connect."

// NoUserMessage is what every tool answers where the connection's
// actor resolves to no user of the yard: the server is running, the
// tools are listed, and no tool is served until one resolves (design
// yardr-ts4d 3.1).
const NoUserMessage = "I can't tell who you are to this yard. Run `yardr user list` to see its people, then set `YARDR_ACTOR` or `--actor` to your name in this client's `yardr mcp` configuration and reconnect."

// questionRule is the one rule for a waiting question, said in the
// server's introduction and in both tools a question passes through,
// since a client may show its model the introduction, the tool list or
// both. The person decides: each question is put to them as a pick (a
// client with a picker shows one, any other lists the choices as text),
// and the answer tool is given what they picked or said, never what the
// client chose for them.
const questionRule = "Ask the person about each waiting question one at a time, offering its choices by their labels with \"put it aside\" and \"decline\" beside them; " +
	"answer only with what they picked or said, never choose for them, and never answer a question they have not been shown."

// DefaultWait is how long ask_the_yardmaster holds its call open for a
// client whose limits are measured (Claude Code): a hundred seconds,
// which the yardmaster's ordinary answer, given when it is next
// nudged, usually meets (design yardr-ts4d 3.5). It is short of Claude
// Code's 120 s tool limit, at which the client moves a call still open
// to the background: two minutes met that limit, so every call went
// there. The twenty seconds left are the margin for the call to reach
// the yard and its answer to get back, so the wait ends, with the
// reply or the words that it will come, inside the turn that asked.
const DefaultWait = 100 * time.Second

// ShortWait is the interim wait for every other client, whose tool-call
// limit is still unmeasured: under sixty seconds, so such a client gets
// the "the answer will come" ending sooner rather than an error. A
// later measurement sets these numbers and nothing else.
const ShortWait = 50 * time.Second

// ToolDef is one tool of the contract: its name in the person's words,
// what it tells the client it does, and its input and output schemas as
// JSON. The schemas are data, beside the names, so another adapter (the
// edge) serves the same contract and a parity check compares bytes, not
// behaviour re-derived per adapter.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	// OutputSchema is the structured part's shape. Every one requires
	// text, the result's words whole (Result.Wire), and leaves the rest
	// open, so a field a later change adds is no break.
	OutputSchema json.RawMessage
}

// Tools is the contract: the eight tools, in the order a client lists
// them. Adding a tool is a contract change (a new ToolDef here, a
// Service method, and the parity both adapters check), never an
// adapter-local one.
func Tools() []ToolDef {
	return []ToolDef{
		{
			Name: ToolWhatIsHappening,
			Description: "Say what is happening with this person's own work: each piece with the project it belongs to and where it stands. " +
				"A piece is finished lately, stuck (on hold, or waiting for other work to finish), parked (set aside for now), " +
				"waiting (for a person to decide, to accept it or to send it to build, or for it to be combined with other small work), not started, or under way. " +
				"Pass the work name from an earlier answer to show that one thing in full, with the notes on it. " +
				"since is a time (for example 2026-11-01T09:00:00Z) widening how far back finished work is shown; without it, the last day.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"work": {"type": "string", "description": "the work name from an earlier answer, to show that one thing in full"},
					"since": {"type": "string", "description": "a time, how far back finished work is shown (default: the last day)"}
				},
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"work_list": {"type": "array", "description": "the account's pieces of work: work, title, project (where it has one), state, detail (the words that say why or when), updated, waiting_questions", "items": {"type": "object"}},
				"work": {"type": "string", "description": "the one piece's work name, when work was given"},
				"title": {"type": "string"},
				"state": {"type": "string", "description": "the one piece's state: finished, stuck, parked, waiting, not started or under way"},
				"project": {"type": "string", "description": "the project the one piece belongs to, when it has one"},
				"detail": {"type": "string", "description": "the words that say why or when the one piece stands as it does"},
				"waiting_questions": {"type": "array", "description": "the numbers of the questions on it that wait for this person", "items": {"type": "integer"}}`),
		},
		{
			Name: ToolAskForSomething,
			Description: "File a request as work for this person, in their words. " +
				"Shape it as you would a good request to a person: a short first line naming the thing wanted, then what done looks like, and anything it is not. " +
				"What you send is filed as it stands, so say what finished looks like plainly. " +
				"project names which of the person's projects it belongs to, in their words; leave it out when they have one project, and ask them when you cannot tell.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"request": {"type": "string", "description": "the request in the person's words: a title line, then what done looks like"},
					"project": {"type": "string", "description": "which of the person's projects this belongs to, in their words"}
				},
				"required": ["request"],
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"work": {"type": "string", "description": "the filed work's name, for what_is_happening"},
				"title": {"type": "string"},
				"project": {"type": "string"}`),
		},
		{
			Name: ToolWhatWaitsForMe,
			Description: "List the questions this yard is waiting for this person to answer, oldest first, each in plain words with its choices. " +
				"Each question carries a work name and a question number; the answer tool takes both. " +
				questionRule,
			InputSchema: schema(`{"type": "object", "properties": {}, "additionalProperties": false}`),
			OutputSchema: output(`
				"questions": {"type": "array", "description": "the waiting questions, oldest first", "items": {
					"type": "object",
					"properties": {
						"work": {"type": "string", "description": "the work name, for the answer tool"},
						"question": {"type": "integer", "description": "the question number, for the answer tool"},
						"title": {"type": "string", "description": "the piece of work it stands on"},
						"text": {"type": "string", "description": "the question whole in plain words, as the person is shown it"},
						"asked_by": {"type": "string"},
						"asked_at": {"type": "string", "description": "when it was asked, as 2026-11-01T09:00:00Z"},
						"choices": {"type": "array", "description": "its choices; none where it wants words or an approval", "items": {
							"type": "object",
							"properties": {"number": {"type": "integer"}, "label": {"type": "string"}, "consequence": {"type": "string"}}
						}},
						"recommended": {"type": "integer", "description": "the recommended choice's number, where one is"}
					}
				}}`),
		},
		{
			Name: ToolAnswer,
			Description: "Give this person's answer to one question from what_waits_for_me, by its work name and question number: " +
				"pick a choice by number (words may go with it), give words alone, approve the command a question proposes (approve), " +
				"put it aside for a day (put_aside), or say it is not theirs to decide (decline, with their reason in words). " +
				questionRule + " " +
				"A question that carries weight shows its weight first and asks the person to type its number back before anything is approved. " +
				"A question asking for a secret is never answered here: it is given in the yard's own window, as its answer says.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"work": {"type": "string", "description": "the work name the question stands on, from what_waits_for_me"},
					"question": {"type": "integer", "minimum": 1, "description": "the question number, from what_waits_for_me"},
					"pick": {"type": "integer", "minimum": 1, "description": "the choice's number, from the question's choices"},
					"words": {"type": "string", "description": "words alone, words with a pick, or why a question is declined"},
					"approve": {"type": "boolean", "description": "approve the command this question proposes, so the yard runs it"},
					"put_aside": {"type": "boolean", "description": "put the question aside for a day"},
					"decline": {"type": "boolean", "description": "end the question with no choice picked: not mine to decide"}
				},
				"required": ["work", "question"],
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"work": {"type": "string"},
				"question": {"type": "integer"},
				"state": {"type": "string", "description": "answered, put aside or declined"}`),
		},
		{
			Name: ToolAskTheYardmaster,
			Description: "Write to the person who runs this yard for you (the yardmaster) and wait a little for the reply, as writing in the yard's chat does. " +
				"If the reply takes longer than the wait, it comes as mail: read_my_mail reads it then. Stopping the wait never takes the message back. " +
				"Pass wait as false for a note or an FYI that needs no answer now: the message is sent, the call returns at once, and any reply comes as mail.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "what to say to the yardmaster, in the person's words"},
					"wait": {"type": "boolean", "description": "false sends the message and returns at once, for a note or an FYI that needs no answer now (default: true, wait a little for the reply)"}
				},
				"required": ["message"],
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"waiting": {"type": "boolean", "description": "true while the reply has not come; it will come as mail"},
				"reply": {"type": "string", "description": "the yardmaster's reply, where it came"}`),
		},
		{
			Name: ToolReadMyMail,
			Description: "Read everything in this person's inbox they have not read yet, from everyone who wrote to them, and mark just what was shown as read. " +
				"What a later call returns is what arrived since. " +
				"Pass recent, a number, to look back at that many recent messages instead, read or not; a look-back marks nothing read.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"recent": {"type": "integer", "minimum": 1, "description": "how many recent messages to show, read or not, marking nothing read"}
				},
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"messages": {"type": "array", "description": "the messages shown: from, at, text, and for a look-back read, whether it had already been read", "items": {"type": "object"}}`),
		},
		{
			Name: ToolReword,
			Description: "Change the words of a request this person filed: its title, its details, or both. " +
				"Only a request they filed themselves can be reworded, and only while nobody is working on it: once someone is, write to the yardmaster instead. " +
				"What you send replaces the old words as it stands, so send the whole new text, and leave out what stays as it is. At least one of title and body is needed. " +
				"work is the work name from an earlier answer.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"work": {"type": "string", "description": "the work name from an earlier answer, of a request this person filed"},
					"title": {"type": "string", "description": "the new title: one line naming the thing wanted"},
					"body": {"type": "string", "description": "the new details: what done looks like, and anything it is not"}
				},
				"required": ["work"],
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"work": {"type": "string", "description": "the reworded work's name"},
				"title": {"type": "string", "description": "the title it has now"}`),
		},
		{
			Name: ToolWithdraw,
			Description: "Withdraw a request this person filed: it is closed, and their reason is kept on it. " +
				"Only a request they filed themselves can be withdrawn; for any other, write to the yardmaster. " +
				"If someone is working on it, they are stopped, and it cannot be undone from here, so be sure the person means it. " +
				"A request whose change is being landed (put in for good) cannot be withdrawn here: write to the yardmaster instead. " +
				"work is the work name from an earlier answer, and reason is why, in the person's words: ask them for it when they have not said.",
			InputSchema: schema(`{
				"type": "object",
				"properties": {
					"work": {"type": "string", "description": "the work name from an earlier answer, of a request this person filed"},
					"reason": {"type": "string", "description": "why it is withdrawn, in the person's words"}
				},
				"required": ["work", "reason"],
				"additionalProperties": false
			}`),
			OutputSchema: output(`
				"work": {"type": "string", "description": "the withdrawn work's name"},
				"title": {"type": "string"}`),
		},
	}
}

// schema keeps a ToolDef's input schema readable beside its
// description; the bytes are the contract, so they are written once,
// as JSON, and served as they stand.
func schema(s string) json.RawMessage { return json.RawMessage(s) }

// output is a tool's output schema: text, which every result carries
// (Result.Wire), and the tool's own properties beside it.
func output(properties string) json.RawMessage {
	return schema(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "the result's words whole, as the person is to be told them"},` + properties + `
				},
				"required": ["text"]
			}`)
}

// Yard is what the tools ask of the yard: the store calls of the
// design (docs/api.md's list, read as the acting user), and no more.
// Notably absent is any call that gives a secret: the MCP gate (no
// secret through MCP) is that there is no call here to make.
type Yard interface {
	Settings(ctx context.Context) (map[string]string, error)
	Crew(ctx context.Context) ([]store.CrewMember, error)
	Depots(ctx context.Context) ([]store.Depot, error)
	CreateBead(ctx context.Context, nb store.NewBead, actor string) (*store.Bead, error)
	UpdateBead(ctx context.Context, id string, p store.BeadPatch, exp store.Expect, actor string) (*store.Bead, error)
	CloseBead(ctx context.Context, id, reason string, exp store.Expect, actor string) (*store.Bead, error)
	ListBeads(ctx context.Context, f store.BeadFilter) ([]store.Bead, error)
	GetBead(ctx context.Context, id string) (*store.Bead, error)
	Bead(ctx context.Context, id string, viewer ...string) (*store.BeadView, error)
	OpenBlockers(ctx context.Context, id string) ([]string, error)
	UserAsks(ctx context.Context, name, actor string, viewer ...string) (*store.AskQueue, error)
	AnswerAsk(ctx context.Context, id string, ask uint64, option int, words, actor string, weighed bool) error
	DeferAsk(ctx context.Context, id string, ask uint64, actor string) error
	DeclineAsk(ctx context.Context, id string, ask uint64, words, actor string) error
	ApproveAsk(ctx context.Context, id string, ask uint64, actor string, weighed bool, values map[string]string) (*store.AskApproved, error)
	Mail(ctx context.Context, to, text, actor string, files ...store.BlobFile) error
	Head(ctx context.Context) (uint64, error)
	UserInbox(ctx context.Context, name string, limit int, actor string) (*store.Mailbox, error)
	AckUserInbox(ctx context.Context, name string, through uint64, actor string) error
}

// A store.API satisfies Yard as it stands.
var _ Yard = (store.API)(nil)

// Service is the tools over one connection: the yard, the user every
// call is made as ("" where the connection's actor resolved to no user
// of the yard, in which case every tool refuses with NoUserMessage),
// and the connection's name, carried on every call so the yard's
// history marks its writes "user via connection". A Service is one
// connection's; an adapter with several connections holds several.
type Service struct {
	Yard Yard
	// User is the yard user every call is made as.
	User string
	// Connection is the connection's name (yardr mcp --connection),
	// "" for an unnamed connection: its writes then read as the user
	// alone.
	Connection string
	// Now is the clock the windows are measured from; nil is the
	// machine's.
	Now func() time.Time
	// Wait is how long AskTheYardmaster holds its call open; an
	// adapter sets it per client (DefaultWait or ShortWait), and
	// zero here is DefaultWait.
	Wait time.Duration
	// PollInterval is how often the wait looks for the reply, and
	// ProgressInterval how often it says it is still waiting; zero
	// is a second and fifteen seconds. Tests shorten both.
	PollInterval     time.Duration
	ProgressInterval time.Duration
}

// Result is one tool's answer: the words written for the person, and,
// beside them, the structured part a program needs (which thing, which
// question) for the next call. The identifiers ride there, not in the
// words the person is shown (design yardr-ts4d 3.5). The exception is
// ask_the_yardmaster: its structured part carries its words too, as
// message, because a client that shows only that part showed nothing
// else of a wait.
type Result struct {
	Text       string
	Structured map[string]any
	// Weighed is set, in place of a finished answer, where the answer
	// approves a card that carries weight and its person has not yet
	// typed its number back: the adapter shows Weighed's form to the
	// person (an elicitation, in whichever shape the client's
	// revision carries one) and calls again with the typed number
	// weighed, as Answer's weighed argument says.
	Weighed *WeighedPrompt
}

// Wire is the result as an adapter sends it: its words, with footer
// (the unread-mail line, "" for none) after them, and its structured
// part with those same words as "text". A client may hand its model
// the structured part alone, and the words are what the person is to
// be told, so every adapter sends through this one place and no tool
// can leave them out.
func (r *Result) Wire(footer string) (text string, structured map[string]any) {
	text = r.Text
	if footer != "" {
		text += "\n\n" + footer
	}
	structured = make(map[string]any, len(r.Structured)+1)
	for k, v := range r.Structured {
		structured[k] = v
	}
	structured["text"] = text
	return text, structured
}

// WeighedPrompt is the form a weighty answer asks its person to pass:
// the words that show the card's whole weight, and the question's
// number, which is what the person types back into it.
type WeighedPrompt struct {
	Number  uint64
	Message string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ctx is the context a call to the yard is made under: the caller's,
// with the connection's name on it where it has one, so the socket's
// server puts the name on the call and the store marks the writes made
// under it (store.WithConnection, carried by rpc's ConnectionHeader).
func (s *Service) ctx(ctx context.Context) context.Context {
	if s.Connection != "" {
		return store.WithConnection(ctx, s.Connection)
	}
	return ctx
}

// requireUser is every tool's first step: no user, no tool served,
// with the one message that names the way on.
func (s *Service) requireUser() error {
	if s.User == "" {
		return fmt.Errorf("%s", NoUserMessage)
	}
	return nil
}

// UnreadCount is the person's unread mail count, as the footer of
// every result carries it: the whole inbox's, from every sender,
// worked out as ReadMyMail works it out (one cursor for all senders).
func (s *Service) UnreadCount(ctx context.Context) (int, error) {
	if err := s.requireUser(); err != nil {
		return 0, err
	}
	box, err := s.Yard.UserInbox(s.ctx(ctx), s.User, 1, s.User)
	if err != nil {
		return 0, err
	}
	return box.Unread, nil
}

// Footer is the line every tool result ends with while mail waits: the
// phrasing the spike found is repeated by every model ("call this tool
// to read it and tell them what it says"), since nothing the server
// sends between turns reaches the model (design yardr-ts4d 3.5). "" for
// no waiting mail, which is not said.
func Footer(unread int) string {
	switch unread {
	case 0:
		return ""
	case 1:
		return "Note for the assistant: this person has 1 unread message waiting. Call read_my_mail to read it and tell them what it says."
	default:
		return fmt.Sprintf("Note for the assistant: this person has %d unread messages waiting. Call read_my_mail to read them and tell them what it says.", unread)
	}
}
