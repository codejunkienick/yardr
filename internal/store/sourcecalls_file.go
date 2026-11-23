package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/source"
)

// CallFileBound is a file call refused before anything was downloaded
// because the yard's blob bound (blob_max_bytes) is not positive: a
// non-positive bound handed to the download path would stream unbounded
// but for its timeout, so it never reaches it.
const CallFileBound = "file_bound"

// SourceFile is the file part of a call's answer (SourceAnswer.File): the
// blob the server kept for the downloaded bytes, by its hash, the name
// suggested for the caller's working directory, and how many bytes they
// are. It carries no bytes: the command fetches them over the socket.
type SourceFile struct {
	Hash  string `json:"hash"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// callMark is the mark a call carries where calls are listed beside an
// update's: " (file)" for a call that answers with a file, " (update)"
// for one that changes something in the service, "" for a plain read.
func callMark(c *source.Call) string {
	switch {
	case c.Answer.File:
		return " (file)"
	case c.Kind == source.Update:
		return " (" + source.Update + ")"
	}
	return ""
}

// callNames is the declaration's call names, in file order, for a refusal
// that lists what a source has instead. It stands with the file-call
// helpers because Sourcecalls.go is at its size ceiling (see noteText).
func callNames(d *source.Declaration) []string {
	out := make([]string, len(d.Calls))
	for i, c := range d.Calls {
		out[i] = c.Name
	}
	return out
}

// grantTail is what a grant card says a call does there, after naming it
// with its method and path (callSays): a file call writes a file into the
// caller's working directory, an update changes something there, and a
// plain read changes nothing. It ends the card's sentence, full stop and
// all, as the sentences it replaces did.
func grantTail(c *source.Call) string {
	switch {
	case c.Answer.File:
		return ", which writes a file into the caller's working directory."
	case c.Kind == source.Update:
		return ", which changes something there."
	}
	return ", which the declaration says reads there and changes nothing."
}

// fileBound is the yard's blob bound as a file call must be given it: the
// stored blob_max_bytes read as it stands, DefaultBlobMax where it is
// unset. A stored value that is no positive number refuses the call
// before anything is downloaded, in words naming the bound: the setting
// is the operator's to fix, and the download is never handed it.
func fileBound(tx *bolt.Tx) (limit int64, refused string) {
	var raw string
	ok, err := get(tx, bSettings, BlobMaxSetting, &raw)
	if err != nil || !ok {
		return DefaultBlobMax, ""
	}
	if n, perr := strconv.ParseInt(raw, 10, 64); perr == nil && n > 0 {
		return n, ""
	}
	return 0, fmt.Sprintf("the yard's %s is %s, not a positive number of bytes: a file call is refused before anything is downloaded (yardr settings set %s <bytes>)", BlobMaxSetting, strconv.Quote(raw), BlobMaxSetting)
}

// fetchFileCall makes the file call call of d through the download path,
// under limit counted on the stream, into a server file below the yard's
// tmp, and answers its result and its bytes where it succeeded. The
// server is told no caller path: it stages below its own home, keeps the
// bytes as a blob (CallSource), and removes the staging either way. A
// refusal the service answered (a Result that is no success) comes back
// as the result, with no bytes.
func (s *Store) fetchFileCall(ctx context.Context, d *source.Declaration, call *source.Call, params map[string]string, credential string, inputs map[string][]byte, limit int64) (*source.Result, []byte, error) {
	root := filepath.Join(s.home, "tmp")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, nil, &OperationalError{Err: err}
	}
	dir, err := os.MkdirTemp(root, "call-")
	if err != nil {
		return nil, nil, &OperationalError{Err: err}
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "file")
	var res *source.Result
	switch {
	case len(call.Build) > 0:
		body, berr := source.BuildBody(ctx, d, call.Name, params, credential, s.home, ReadSecretFile)
		if berr != nil {
			return nil, nil, berr
		}
		res, err = source.DoFileBody(ctx, d, call.Name, params, credential, body, dest, limit, ReadSecretFile)
	case inputs != nil:
		res, err = source.DoFileWithInputs(ctx, d, call.Name, params, credential, inputs, dest, limit, ReadSecretFile)
	default:
		res, err = source.DoFile(ctx, d, call.Name, params, credential, dest, limit, ReadSecretFile)
	}
	if err != nil || res == nil || !res.OK() {
		return res, nil, err
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		return nil, nil, &OperationalError{Err: err}
	}
	return res, data, nil
}

// keepFileAnswer keeps a downloaded file's bytes as a blob and answers
// the file part of the call that fetched them: the blob's hash and size,
// and the suggested name for the caller's directory (suggestedFileName).
func (s *Store) keepFileAnswer(ctx context.Context, src, call string, res *source.Result, data []byte) (*SourceFile, error) {
	ref, err := s.PutBlob(ctx, data)
	if err != nil {
		return nil, err
	}
	return &SourceFile{Hash: ref.Hash, Name: suggestedFileName(src, call, res.Filename, data), Bytes: ref.Bytes}, nil
}

// suggestedFileName is the name a file call's answer suggests for the
// caller's working directory: the final download's name, cut to its last
// segment and held to CheckBlobName, and where that fails, names
// nothing or names a hidden file, the yard's own <source>-<call> with the
// extension the bytes' type suggests. No outside name picks a directory:
// the command writes in its own, under this name alone. A hidden name is
// refused because the names tools read as their own settings in a
// working directory (.envrc, .mcp.json, .npmrc, .git) are hidden ones, and
// a service's bytes must never land there as one.
func suggestedFileName(src, call, suggested string, data []byte) string {
	name := suggested
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name != "" && !strings.HasPrefix(name, ".") && CheckBlobName(name) == nil {
		return name
	}
	return src + "-" + call + fileExt(data)
}

// fileExt is the extension the bytes' own type suggests, for the yard's
// fallback file name: the type is read from the bytes, never trusted for
// more than this name.
func fileExt(data []byte) string {
	mime, _, _ := strings.Cut(blobType(data), ";")
	switch strings.TrimSpace(mime) {
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "text/html":
		return ".html"
	case "text/plain":
		return ".txt"
	case "application/json":
		return ".json"
	case "application/zip":
		return ".zip"
	}
	return ".bin"
}

// FileAnswerLine is the one line a file answer is printed and noted
// under its marking: the file the command wrote, in its working
// directory, and how many bytes it holds.
func FileAnswerLine(name string, n int64) string {
	return fmt.Sprintf("file: ./%s (%s bytes)", name, commaCount(n))
}

// commaCount is n with its thousands apart, as a person reads a file's
// size on a terminal.
func commaCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// noteText is the answer as the note of a bead: under the line every
// answer is shown under, indented as yardr call prints it. A file answer
// notes its marking and its file line, and no bytes: the blob on the
// server, named by its hash in the answer, is the durable copy.
func noteText(a *SourceAnswer) string {
	if a.File != nil {
		return a.Marking + "\n" + FileAnswerLine(a.File.Name, a.File.Bytes)
	}
	if len(a.Answer) == 0 {
		return fmt.Sprintf("%s\n(%d, and nothing in the answer)", a.Marking, a.Status)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, a.Answer, "", "  ") != nil {
		return a.Marking + "\n" + string(a.Answer)
	}
	return a.Marking + "\n" + pretty.String()
}
