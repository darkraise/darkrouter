// Package sse implements the subset of the WHATWG EventSource wire format that
// LLM providers actually use, plus the OpenAI "[DONE]" sentinel.
package sse

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Done is the OpenAI-dialect stream terminator. It is not a JSON payload.
const Done = "[DONE]"

var ErrLineTooLong = errors.New("sse: line exceeds configured maximum")

type Event struct {
	Type  string
	Data  string
	ID    string
	Retry string
}

type Reader struct {
	sc *bufio.Scanner
}

// NewReader reads events from r, rejecting any line longer than maxLine bytes.
func NewReader(r io.Reader, maxLine int) *Reader {
	sc := bufio.NewScanner(r)
	// The effective cap is max(maxLine, cap(buf)), so the initial buffer must
	// not exceed maxLine or a small cap silently stops being enforced.
	sc.Buffer(make([]byte, 0, min(4096, maxLine)), maxLine)
	sc.Split(splitLines)
	return &Reader{sc: sc}
}

// splitLines splits on LF, CRLF, and a lone CR, all of which the SSE grammar
// permits as line terminators.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			// A trailing CR may be half of a CRLF; wait for more bytes.
			return 0, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Next returns the next dispatched event, or io.EOF when the stream ends.
func (r *Reader) Next() (Event, error) {
	var (
		ev   Event
		data []string
		seen bool
	)
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if !seen {
				continue // blank line with nothing buffered dispatches nothing
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue // comment, e.g. OpenRouter's ": OPENROUTER PROCESSING"
		}
		seen = true
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue // a bare field name carries an empty value we do not use
		}
		value = strings.TrimPrefix(value, " ") // exactly one optional space
		switch field {
		case "data":
			data = append(data, value)
		case "event":
			ev.Type = value
		case "id":
			ev.ID = value
		case "retry":
			ev.Retry = value
		}
	}
	if err := r.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return Event{}, ErrLineTooLong
		}
		return Event{}, err
	}
	if seen {
		ev.Data = strings.Join(data, "\n")
		return ev, nil
	}
	return Event{}, io.EOF
}
