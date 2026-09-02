package adapter

import (
	"fmt"
	"strings"
)

// EscapePathSegment percent-encodes everything outside RFC 3986's unreserved
// set.
//
// url.PathEscape is not equivalent: it leaves ':' alone, which is legal in a
// path segment but is not what AWS signs. smithy-go's own EscapePath says it
// outright — "AWS expects every character except these to be escaped" — and
// since every Bedrock inference-profile id contains a colon, getting this wrong
// is a 403 on every request with no cause attached. Vertex does not sign the
// path, but a model id carrying a colon would otherwise open a path segment the
// API does not match, so both kinds share this one rule.
func EscapePathSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
