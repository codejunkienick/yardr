package standin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// LinearState is a workflow state of the stand-in's one Linear team.
type LinearState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

// LinearStates are the statuses a new Linear team has, with In Review added
// as most teams do.
var LinearStates = []LinearState{
	{"s-backlog", "Backlog", "backlog", 0},
	{"s-todo", "Todo", "unstarted", 1},
	{"s-progress", "In Progress", "started", 2},
	{"s-review", "In Review", "started", 3},
	{"s-done", "Done", "completed", 4},
	{"s-canceled", "Canceled", "canceled", 5},
	{"s-duplicate", "Duplicate", "canceled", 6},
}

// LinearTeam is the stand-in's one Linear team: what teams lists, and what
// an issue is opened in.
var LinearTeam = struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}{ID: "t-yrd", Key: "YRD", Name: "Yardr"}

// LinearComment is a comment on a Linear issue. Parent is the id of the
// comment it answers, "" for a top-level one. By names who made it, or,
// for Bot, the app it was made as.
type LinearComment struct {
	ID     string    `json:"id"`
	Body   string    `json:"body"`
	Parent string    `json:"parent,omitempty"`
	At     time.Time `json:"at"`
	By     string    `json:"by,omitempty"`
	ByID   string    `json:"byID,omitempty"`
	Bot    bool      `json:"bot,omitempty"`
}

// A file reaches a Linear comment in two exchanges, which the stand-in
// serves as Linear's upload guide describes them (read 16 November 2026):
// fileUpload on the GraphQL endpoint, with the file's type, name and size,
// answers where to put it (uploadUrl, a signed address on a host that is
// not the API), what the PUT must carry (headers) and the address the
// stored file will have (assetUrl); then the file goes to the signed
// address by PUT, and the comment holds the asset's address in its
// markdown. The signed address and the headers are secrets of the exchange,
// so the stand-in is as strict about them as a storage host is: the
// signature is in the address, the type and the named headers must come as
// named, the body must be as long as fileUpload was told, and a PUT that
// carries an Authorization header is refused, since the signature is the
// only credential such an address takes.

// linearCacheControl is the one header the guide's example sets itself
// besides the type and the ones fileUpload names.
const linearCacheControl = "public, max-age=31536000"

// LinearHeader is a header fileUpload tells a client to send with the PUT.
type LinearHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// LinearUpload is a file fileUpload was asked for, and what came of sending
// it to its address. The first failed attempt of a reply leaves its uploads
// behind, referenced by no comment, as Linear's would be.
type LinearUpload struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	// URL and Headers are what the client was given for the PUT: a client
	// keeps both out of every argument list, answer and log.
	URL     string         `json:"url"`
	Headers []LinearHeader `json:"headers"`
	// AssetURL is where the file will be. It ends in the file's name, as a
	// storage service's often does, so that a name made of markdown's own
	// characters reaches the address a comment links to as well as the text.
	AssetURL string `json:"assetUrl"`
	// Sent is whether a PUT of the file was taken, and SHA256 that of the
	// bytes it carried. Bearer is whether any PUT of it carried an
	// Authorization header.
	Sent   bool   `json:"sent"`
	SHA256 string `json:"sha256,omitempty"`
	Bearer bool   `json:"bearer,omitempty"`

	sig string // the signature in URL's query
}

// linearFiles is what the stand-in keeps of the files asked of fileUpload
// and how it answers them.
type linearFiles struct {
	uploads []LinearUpload
	refuse  map[string]int // the status a file's PUT is answered with, by its name
	headers []LinearHeader // what fileUpload names for the PUT, nil for the default
}

// LinearUploads are the files fileUpload was asked for, oldest first.
func (s *Server) LinearUploads() []LinearUpload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.files.uploads)
}

// linearTake files c on the issue i, or, where a comment with c's id
// already stands, files nothing: Linear takes the caller's id as the
// comment's own, so a retry with the same key posts once, and the second
// call is answered with the comment the first left. The caller holds s.mu.
func (s *Server) linearTake(i *Issue, c LinearComment) (*Issue, LinearComment) {
	if oldIssue, dup := s.findComment(c.ID); dup != nil {
		s.calls = append(s.calls, "linear comment "+i.Key+" "+c.ID)
		return oldIssue, *dup
	}
	i.LinearComments = append(i.LinearComments, c)
	s.calls = append(s.calls, "linear comment "+i.Key)
	s.save()
	return i, c
}

// RefuseLinearUpload makes the PUT of the file called filename answer
// status, with a body that gives the address and the headers back as a
// storage host's error page does, until it is told 0. A status in the 300s
// also names a place to go instead, which the stand-in notes if a client
// goes there.
func (s *Server) RefuseLinearUpload(filename string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == 0 {
		delete(s.files.refuse, filename)
		return
	}
	if s.files.refuse == nil {
		s.files.refuse = map[string]int{}
	}
	s.files.refuse[filename] = status
}

// SetLinearUploadHeaders sets the headers fileUpload names for the PUT of a
// file asked for from now on, for a trial of what a client does with one it
// cannot send. nil restores the default.
func (s *Server) SetLinearUploadHeaders(h []LinearHeader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files.headers = h
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// linearSlot answers fileUpload with the variables it was sent: the place
// the file goes, or the words of its refusal. The caller holds s.mu.
func (s *Server) linearSlot(r *http.Request, vars map[string]any) (any, string) {
	contentType, _ := vars["contentType"].(string)
	filename, _ := vars["filename"].(string)
	size, isNumber := vars["size"].(float64)
	switch {
	case contentType == "":
		return nil, "contentType: a type is required"
	case filename == "" || strings.ContainsAny(filename, `/\`):
		return nil, "filename: a name is required, and a name is never a path"
	case !isNumber || size < 0 || size != float64(int(size)):
		return nil, "size: a whole number of bytes is required"
	}
	// The default names two headers, the second odd on purpose: a client
	// that writes them into a config of its own must quote them.
	headers := s.files.headers
	if headers == nil {
		headers = []LinearHeader{
			{"x-linear-upload-token", "up-" + randomHex(8)},
			{"x-linear-note", `a "quoted" word and a \ backslash`},
		}
	}
	id := fmt.Sprintf("u-%d", len(s.files.uploads)+1)
	u := LinearUpload{ID: id, Filename: filename, ContentType: contentType, Size: int(size), Headers: headers,
		AssetURL: "https://uploads.linear.app/standin/" + id + "/" + filename, sig: randomHex(16)}
	u.URL = "http://" + r.Host + "/uploads/" + id + "?sig=" + u.sig
	s.files.uploads = append(s.files.uploads, u)
	s.calls = append(s.calls, fmt.Sprintf("linear upload %s %s %d", filename, contentType, u.Size))
	return map[string]any{"fileUpload": map[string]any{"success": true,
		"uploadFile": map[string]any{"uploadUrl": u.URL, "assetUrl": u.AssetURL, "headers": u.Headers,
			"filename": filename, "contentType": contentType, "size": u.Size}}}, ""
}

// linearPut takes the file a signed address names, or refuses it as a
// storage host does: with its status and a page that repeats what the
// request held, the address and the headers among it.
func (s *Server) linearPut(w http.ResponseWriter, r *http.Request) {
	// The body is read before the lock is taken, so a slow client holds up
	// no other call.
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	s.mu.Lock()
	defer s.mu.Unlock()
	id, moved, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/uploads/"), "/")
	i := slices.IndexFunc(s.files.uploads, func(u LinearUpload) bool { return u.ID == id })
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	u := &s.files.uploads[i]
	// The place a refusal sends a client to. A client that went there was
	// told to follow a redirect, and the call log says so.
	if moved == "moved" {
		s.calls = append(s.calls, "linear put followed "+u.Filename)
		return
	}
	answer := func(status int) {
		s.calls = append(s.calls, fmt.Sprintf("linear put %s %d", u.Filename, status))
		w.Header().Set("Content-Type", "application/json")
		if status/100 == 3 {
			w.Header().Set("Location", "/uploads/"+u.ID+"/moved?sig="+u.sig)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": r.URL.RequestURI(), "headers": r.Header})
	}
	switch {
	case r.Method != http.MethodPut:
		http.Error(w, "an upload address takes a PUT", http.StatusMethodNotAllowed)
	case r.URL.Query().Get("sig") != u.sig:
		answer(http.StatusForbidden)
	case r.Header.Get("Authorization") != "":
		u.Bearer = true
		answer(http.StatusBadRequest)
	case s.files.refuse[u.Filename] != 0:
		answer(s.files.refuse[u.Filename])
	case r.Header.Get("Content-Type") != u.ContentType, r.Header.Get("Cache-Control") != linearCacheControl,
		slices.ContainsFunc(u.Headers, func(h LinearHeader) bool { return r.Header.Get(h.Key) != h.Value }):
		answer(http.StatusForbidden)
	case len(body) != u.Size:
		answer(http.StatusBadRequest)
	default:
		sum := sha256.Sum256(body)
		u.Sent, u.SHA256 = true, hex.EncodeToString(sum[:])
		s.calls = append(s.calls, "linear put "+u.Filename+" 200")
	}
}
