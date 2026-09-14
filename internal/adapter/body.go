package adapter

import (
	"errors"
	"io"
)

// MaxResponseBytes bounds a unary chat response. Nothing configurable reaches a
// parser, and the parsers that already carried a bound use this figure.
const MaxResponseBytes = 32 << 20

var ErrResponseTooLarge = errors.New("adapter: upstream response exceeds the size limit")

// ReadResponse reads a whole unary body, reporting one past MaxResponseBytes as
// ErrResponseTooLarge rather than handing back a truncated prefix.
func ReadResponse(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return raw, nil
}
