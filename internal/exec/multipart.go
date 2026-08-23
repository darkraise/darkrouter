package exec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Form is a multipart request body held whole.
//
// Buffered, not streamed, per spec §6. A streamed body cannot be replayed for a
// second attempt, which would make transcriptions the one surface with no
// failover — and the rewrite the router requires is impossible while streaming
// anyway, because the model field lives inside the form and clients are free to
// place it after the file part. Buffering restores failover, makes the rewrite
// trivial, and lets an oversized upload be refused before any upstream
// connection exists.
type Form struct {
	parts []formPart
}

type formPart struct {
	name     string
	filename string
	header   textproto.MIMEHeader
	value    []byte
}

// ParseForm reads the whole body, enforcing max while reading rather than
// after, so a client cannot make the gateway allocate more than the operator
// allowed by lying about Content-Length.
func ParseForm(r *http.Request, max int64) (*Form, error) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil, fmt.Errorf("invalid Content-Type: %w", err)
	}
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("expected multipart/form-data, got %s", mediaType)
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, errors.New("multipart body has no boundary")
	}

	// One budget across every part: capping each part separately would let a
	// client send a thousand parts each just under the limit.
	remaining := max
	mr := multipart.NewReader(r.Body, boundary)
	f := &Form{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read multipart body: %w", err)
		}
		// One byte past the budget is enough to know it was exceeded, and stops
		// the read there rather than draining the rest of the upload.
		buf, err := io.ReadAll(io.LimitReader(p, remaining+1))
		p.Close()
		if err != nil {
			return nil, fmt.Errorf("read multipart part %q: %w", p.FormName(), err)
		}
		if int64(len(buf)) > remaining {
			return nil, &ir.Error{
				Type:    ir.ErrPayloadTooLarge,
				Message: fmt.Sprintf("upload exceeds the configured maximum of %d bytes", max),
			}
		}
		remaining -= int64(len(buf))
		f.parts = append(f.parts, formPart{
			name: p.FormName(), filename: p.FileName(),
			// textproto.MIMEHeader has no Clone; only http.Header does. A
			// shallow copy is right here because the values are never mutated,
			// and the header must be copied because the reader reuses the part.
			header: maps.Clone(p.Header), value: buf,
		})
	}
	if len(f.parts) == 0 {
		return nil, errors.New("multipart body has no parts")
	}
	return f, nil
}

// Field returns a non-file field's value, or "" when it is absent.
func (f *Form) Field(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename == "" {
			return string(p.value)
		}
	}
	return ""
}

// File returns a file part's contents, or "" when it is absent.
func (f *Form) File(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename != "" {
			return string(p.value)
		}
	}
	return ""
}

// FileName returns a file part's declared filename. Whisper providers select a
// decoder from its extension, so dropping it turns a working upload into an
// unsupported-format error.
func (f *Form) FileName(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename != "" {
			return p.filename
		}
	}
	return ""
}

// Render writes the form out with model rewritten to the target's name, adding
// the field when the client sent none. It is called once per attempt, which is
// what makes failover across two differently-named models possible.
func (f *Form) Render(model string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	wrote := false
	for _, p := range f.parts {
		if p.name == "model" && p.filename == "" {
			if err := w.WriteField("model", model); err != nil {
				return nil, "", err
			}
			wrote = true
			continue
		}
		// CreatePart rather than CreateFormFile: the original part's header
		// carries the Content-Type a provider may use to pick a decoder, and
		// CreateFormFile would replace it with application/octet-stream.
		pw, err := w.CreatePart(p.header)
		if err != nil {
			return nil, "", err
		}
		if _, err := pw.Write(p.value); err != nil {
			return nil, "", err
		}
	}
	if !wrote {
		if err := w.WriteField("model", model); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}
