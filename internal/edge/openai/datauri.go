package openai

import (
	"encoding/base64"
	"mime"
	"path/filepath"
	"strings"
)

// splitDataURI pulls the MIME type and base64 payload out of a data URI. A
// value that is not one is returned as opaque data, since some clients send
// bare base64.
func splitDataURI(s string) (mimeType, data string) {
	rest, ok := strings.CutPrefix(s, "data:")
	if !ok {
		return "", s
	}
	head, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", s
	}
	return dataURIParts(head, payload)
}

// dataURIParts reads a data URI's metadata and payload. Without ;base64 the
// payload is percent-encoded bytes (RFC 2397), and the IR carries base64, so it
// is decoded and re-encoded rather than passed along as if it were base64.
func dataURIParts(head, payload string) (mimeType, data string) {
	params := strings.Split(head, ";")
	mimeType = strings.TrimSpace(params[0])
	if mimeType == "" {
		mimeType = "text/plain"
	}
	if strings.EqualFold(params[len(params)-1], "base64") && len(params) > 1 {
		return mimeType, payload
	}
	return mimeType, base64.StdEncoding.EncodeToString(percentDecode(payload))
}

// percentDecode leaves a malformed escape as its literal bytes, as the WHATWG
// URL standard does, rather than rejecting a payload a browser would accept.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	default:
		return c - '0'
	}
}

// documentTypes names the document formats Bedrock Converse's DocumentBlock
// accepts. They are fixed here rather than left to mime.TypeByExtension, whose
// answer depends on the host's MIME tables: the release image has none, and Go's
// built-in table has no .md.
var documentTypes = map[string]string{
	".pdf":      "application/pdf",
	".txt":      "text/plain",
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".csv":      "text/csv",
	".html":     "text/html",
	".htm":      "text/html",
	".doc":      "application/msword",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":      "application/vnd.ms-excel",
	".xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// fileMedia splits an inline file's data and, when the payload names no type,
// takes one from the filename: a document with no MIME type is one most
// targets cannot accept.
func fileMedia(fileData, filename string) (mimeType, data string) {
	mimeType, data = splitDataURI(fileData)
	if mimeType == "" && filename != "" {
		ext := strings.ToLower(filepath.Ext(filename))
		if t, ok := documentTypes[ext]; ok {
			return t, data
		}
		mimeType, _, _ = strings.Cut(mime.TypeByExtension(ext), ";")
	}
	return mimeType, data
}
