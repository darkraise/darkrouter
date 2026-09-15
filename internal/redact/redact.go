// Package redact removes credentials from error text before it is stored,
// logged or shown to anyone.
package redact

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// minSecretLen is the shortest secret replaced by its text. Every occurrence
// is replaced, and a secret short enough to be an ordinary word would rewrite
// the words around it and hide the reason for the failure. A short key in a
// request URL is still removed with the query string.
const minSecretLen = 8

const placeholder = "[redacted]"

// Error returns err with credentials removed from its text: the query string
// of any request URL it quotes, and each secret in the forms error text
// carries it in. What err wraps is unchanged, so errors.Is and errors.As
// classify the result exactly as they would err.
func Error(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		if i := strings.IndexByte(ue.URL, '?'); i >= 0 {
			// url.Error quotes its URL with %q. Replacing the quoted form
			// covers the text however many errors were wrapped around it,
			// since each copied the text when it was made.
			msg = strings.ReplaceAll(msg, unquoted(ue.URL), unquoted(ue.URL[:i]))
		}
	}
	for _, s := range secrets {
		if len(s) < minSecretLen {
			continue
		}
		for _, form := range []string{
			url.QueryEscape(s), url.PathEscape(s), unquoted(s), s,
		} {
			msg = strings.ReplaceAll(msg, form, placeholder)
		}
	}
	if msg == err.Error() {
		return err
	}
	return &redacted{msg: msg, err: err}
}

// unquoted is s as it appears between the quotes of a %q verb.
func unquoted(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}

type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }
