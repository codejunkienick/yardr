package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// callFileJSON is a file answer as --json prints it alone, shaped
// {"file":{"name","bytes"}}: the file the command wrote, its name as
// written and how many bytes it holds, and no bytes of it.
type callFileJSON struct {
	File callFileRef `json:"file"`
}

type callFileRef struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// blobBytes is the one socket method yardr call reaches beyond store.API
// for a file answer (rpc.Client.BlobBytes): the generated client carries
// blob metadata and no bytes, and no other implementer keeps blobs
// behind a socket.
type blobBytes interface {
	BlobBytes(ctx context.Context, hash string) ([]byte, string, error)
}

// fetchCallFile gets the bytes of a file answer's blob (file): over the
// yard's socket where the store is its client, and through the store's
// own record and path where the store is held directly, as in a test, so
// the command's write is tested whole without a socket.
func fetchCallFile(ctx context.Context, st store.API, file *store.SourceFile) ([]byte, error) {
	if b, ok := st.(blobBytes); ok {
		data, _, err := b.BlobBytes(ctx, file.Hash)
		return data, err
	}
	direct, ok := st.(interface {
		Blob(string) (*store.BlobRef, error)
		BlobPath(string) string
	})
	if !ok {
		return nil, fmt.Errorf("the yard this command talks to cannot hand over a file's bytes")
	}
	if _, err := direct.Blob(file.Hash); err != nil {
		return nil, err
	}
	return os.ReadFile(direct.BlobPath(file.Hash))
}

// writeFetchedFile fetches a file answer's blob, holds what came back to
// the size the call recorded, and writes it in the working directory
// under the answer's suggested name, answering the name actually
// written. A short or long fetch is a failed fetch and leaves no file.
func writeFetchedFile(ctx context.Context, st store.API, res *store.SourceAnswer) (string, error) {
	data, err := fetchCallFile(ctx, st, res.File)
	if err != nil {
		return "", err
	}
	if int64(len(data)) != res.File.Bytes {
		return "", fmt.Errorf("the fetched file is %d bytes, and the call recorded %d", len(data), res.File.Bytes)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return writeCallFile(dir, res.File.Name, data)
}

// fileNotWritten is a fetch or write failure after a made file call, in
// the words of a call made and not noted: the call stands in the record,
// and no file stands in the working directory.
func fileNotWritten(res *store.SourceAnswer, why error) error {
	return fmt.Errorf("source %s, call %s: the call was made and recorded and the file not written: %w", res.Source, res.Call, why)
}

// writeCallFile writes data in dir under name, never overwriting: by a
// temporary file in the same directory and a link under the final name,
// so a reader never finds half a file there and two writers of one name
// land as numbered neighbours (name-1.ext), each complete. The file is
// 0600, never executable: the yard writes data. It answers the name
// actually written.
func writeCallFile(dir, name string, data []byte) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if err := store.CheckBlobName(name); err != nil || name == "" {
		if err == nil {
			err = fmt.Errorf("the file's suggested name is empty")
		}
		return "", fmt.Errorf("the suggested name %q may not be written: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, ".yardr-call-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // gone already where the link below stands
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// os.Link fails with EEXIST atomically where the name stands, on
	// Unix and Windows alike: check-then-rename would lose the race of
	// two sessions sharing one directory silently.
	stem, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	for n := 0; ; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		switch err := os.Link(tmpName, filepath.Join(dir, candidate)); {
		case err == nil:
			return candidate, nil
		case !errors.Is(err, os.ErrExist):
			return "", err
		case n >= 10000:
			return "", fmt.Errorf("every numbered neighbour of %s stands already", name)
		}
	}
}

// callKindWord is a call's kind as a list marks it: "file" for a call
// that answers with a file (a read the lists mark apart, as an update),
// else its declared kind.
func callKindWord(c source.Call) string {
	if c.Answer.File {
		return "file"
	}
	return c.Kind
}

// callableMark is the mark a printed callable carries for what it does:
// "(file)" for a file call, "(update)" for an update, "" for a plain
// read, as yardr call --list and a brief each mark it in their spacing.
func callableMark(c store.Callable) string {
	switch {
	case c.File:
		return "(file)"
	case c.Update():
		return "(" + c.Kind + ")"
	}
	return ""
}
