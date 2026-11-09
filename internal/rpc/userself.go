package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// selfArg is, for each call that names whose inbox or queue it reads,
// who reads, or who writes, the arguments that do: over the network each
// is the caller. Conversation names two parties and is checked by itself
// (asUser).
var selfArg = map[string][]string{
	"GetUser":              {"name"},
	"UserInbox":            {"name", "actor"},
	"AckUserInbox":         {"name", "actor"},
	"UserNotifications":    {"name", "actor"},
	"AckUserNotifications": {"name", "actor"},
	"UserAsks":             {"name", "actor", "viewer"},
	"Waiting":              {"name", "actor", "viewer"},
	"MoveBead":             {"actor"},
	"Bead":                 {"viewer"},
	"Arrivals":             {"viewer"},
	"Flows":                {"viewer"},
	"Mail":                 {"actor"},
	"MailPeer":             {"actor"},
	"React":                {"actor"},
	"AnswerAsk":            {"actor"},
	"DeferAsk":             {"actor"},
	"DeclineAsk":           {"actor"},
	"TakeBackAsk":          {"actor"},
	"AddNote":              {"actor"},
	"GiveSecret":           {"actor"},
	"ApproveAsk":           {"actor"},
	"ApproveSetup":         {"actor"},
	"UninviteTeammate":     {"actor"},
	"TelemetrySet":         {"actor"},
}

// asUser is the arguments body of the call method as user makes it: each
// argument that says whose inbox is read or who writes is filled in with
// user when it is left out, and refused when it names anyone else; one side
// of a Conversation is user. The arguments are an object, by name: the
// array is the form of yardr's own builds, and is not taken here.
func asUser(method string, body []byte, user string) (json.RawMessage, error) {
	if b := bytes.TrimLeft(body, " \t\r\n"); len(b) == 0 || b[0] != '{' {
		return nil, fmt.Errorf("arguments: over the network %s takes them as a JSON object, by name", method)
	}
	var given map[string]json.RawMessage
	if err := json.Unmarshal(body, &given); err != nil {
		return nil, fmt.Errorf("arguments: %w", err)
	}
	me, _ := json.Marshal(user)
	// self is the name the argument arg gives, "" for one left out, null or
	// empty. An argument of another type is no name (ok false): it is left
	// as it is, for the call to refuse as it does on the socket, and no
	// call is made with it.
	self := func(arg string) (name string, ok bool) {
		raw, has := given[arg]
		if !has {
			return "", true
		}
		return name, json.Unmarshal(raw, &name) == nil
	}
	for _, arg := range selfArg[method] {
		switch name, ok := self(arg); {
		case !ok:
		case name == "":
			given[arg] = me
		case name != user && isOperatorCall(method):
			// Not said back: such a call may carry a secret, and a client
			// that put it where the name goes would read it in the refusal.
			return nil, fmt.Errorf("this device calls as %s: %q is %s or left out", user, arg, user)
		case name != user:
			return nil, fmt.Errorf("this device calls as %s: %q is %s or left out, not %s", user, arg, user, name)
		}
	}
	if method == "Conversation" {
		a, aok := self("a")
		b, bok := self("b")
		if aok && a == "" {
			given["a"], a = me, user
		}
		if aok && bok && a != user && b != user {
			return nil, fmt.Errorf("this device calls as %s: one of \"a\" and \"b\" is %s", user, user)
		}
	}
	return json.Marshal(given)
}
