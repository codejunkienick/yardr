package rpc

import (
	"errors"
	"net/http"
	"strconv"

	"yardr.dev/yardr/internal/store"
)

// userNoteBlob serves a note's file at its stable address
// (/user/blobs/note/<seq>/<n>) to any device, as userBlob serves a
// note's file by hash: the note's current bytes for that file number,
// so a link a device keeps shows the page after a replace. A seq or
// number that does not parse, a seq no note has and an n the note has
// no file at answer as a missing blob.
func (u *UserServer) userNoteBlob(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.ParseUint(r.PathValue("seq"), 10, 64)
	if err != nil {
		http.Error(w, "no such blob", http.StatusNotFound)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "no such blob", http.StatusNotFound)
		return
	}
	ref, err := u.st.NoteFileBlob(r.Context(), seq, n)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		u.blobErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ref.Type)
	w.Header().Set("ETag", `"`+ref.Hash+`"`)
	http.ServeFile(w, r, u.st.BlobPath(ref.Hash))
}
