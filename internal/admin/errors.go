package admin

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/store"
)

// internalMessage is the whole of what a 500 says to the client. The cause
// is logged: a database path, a driver message or a wrapped upstream error in
// a response body tells an attacker about the deployment and tells an
// operator nothing the log does not.
const internalMessage = "internal error"

// internalError logs the cause and answers with the fixed message.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("admin: %s %s: %v", r.Method, r.URL.Path, err)
	writeError(w, http.StatusInternalServerError, internalMessage)
}

// writeStoreError maps the store's sentinels to statuses in one place. A miss
// is 404 and a taken value is 409, with the store's own message, which names
// the row rather than the table; anything else is a 500.
func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		internalError(w, r, err)
	}
}

// decodeJSON reads one JSON body of at most limit bytes into v, refusing an
// unknown field. A misspelled field silently ignored is a write the operator
// believes happened; refusing it is the only way the mistake surfaces. It
// answers the 400 itself and reports whether the caller may proceed.
func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, decodeMessage(err))
		return false
	}
	// One value only. Trailing content is a second document, which no
	// endpoint accepts.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body: trailing content")
		return false
	}
	return true
}

func decodeMessage(err error) string {
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		return "request body is too large"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return "invalid JSON body: " + strings.TrimPrefix(err.Error(), "json: ")
	}
	return "invalid JSON body"
}
