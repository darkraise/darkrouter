package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// forwardStream pipes a forwarded SSE response to the client, recognizing
// commit, in-stream errors and usage as the bytes go past.
//
// The scan is inline — read a chunk, split it, write it — rather than a
// TeeReader into a goroutine behind a pipe. Spec §7: if the scanner falls
// behind or exits, the pipe write blocks and the client's stream freezes.
// Inline scanning has no concurrency and cannot stall.
//
// strip removes the extra final usage chunk that Darkrouter's own injected
// stream_options produced. When the client asked for usage itself the chunk is
// theirs and removing it would be a fourth mutation.
func (e *Executor) forwardStream(cw *CommitWriter, resp *http.Response, ac *AttemptCtx,
	fw adapter.Forwarder, se streamErrorWriter, strip bool) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()

	cfg, rec, c := ac.Cfg, ac.Rec, ac.Cand
	maxLine := cfg.Server.SSE.MaxLineBytes
	sp := &eventSplitter{max: maxLine}

	var (
		pending      [][]byte
		pendingBytes int
		committed    bool
		usage        ir.Usage
		// failed is an error event the provider sent after commit.
		failed error
	)

	// recordWarning notes a post-commit fault on the row. Failover is
	// impossible once bytes are on the wire, so this is the only place left
	// for the fault to show up.
	recordWarning := func(reason string) {
		rec.Warnings = append(rec.Warnings, ir.Warning{
			Field: "passthrough", Target: c.ProviderID + "/" + c.Model, Reason: reason,
		}.String())
	}

	commit := func() {
		committed = true
		// Before the replay rather than after it, so the replay's writes to
		// the client are held off idle like every later one.
		ac.resetIdle()
		ac.served(ac.Warns)
		copyResponseHeaders(cw.Header(), resp.Header)
		e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
		cw.WriteHeader(resp.StatusCode)
		for _, raw := range pending {
			_, _ = cw.Write(raw)
		}
		pending, pendingBytes = nil, 0
		cw.Flush()
	}

	// step handles one whole event. A non-nil error ends the attempt.
	step := func(raw []byte) (adapter.Outcome, *ir.Error) {
		var re adapter.RawEvent
		if ev, ok := parseEvent(raw, maxLine); ok {
			re = fw.RecognizeEvent(ev)
		}
		if re.Usage != nil {
			mergeUsage(&usage, re.Usage)
			applyUsage(rec, &usage)
		}

		if !committed {
			if re.ErrPayload != "" {
				return ac.reclassifyStream(forwardedStreamError(fw, raw, maxLine, re.ErrPayload))
			}
			if re.Content {
				commit()
				_, _ = cw.Write(raw)
				cw.Flush()
				return adapter.OutcomeSuccess, nil
			}
			if cap := cfg.Server.SSE.MaxPrecommitBytes; cap > 0 && pendingBytes+len(raw) > cap {
				return ac.reclassifyStream(ErrPreCommitBufferFull)
			}
			// Counted against the cap either way — a flood shaped like the
			// injected summary must still trip it — but only kept for replay
			// when it is not that summary. Buffering it here would let it
			// survive into an empty completion's commit, where nothing else
			// distinguishes it from a chunk the client actually asked to see.
			pendingBytes += len(raw)
			if strip && re.UsageOnly {
				return adapter.OutcomeSuccess, nil
			}
			pending = append(pending, raw)
			return adapter.OutcomeSuccess, nil
		}

		if re.ErrPayload != "" && failed == nil {
			// Forwarded verbatim like every event after commit, and still
			// the provider failing this response.
			failed = forwardedStreamError(fw, raw, maxLine, re.ErrPayload)
		}
		if strip && re.UsageOnly {
			return adapter.OutcomeSuccess, nil
		}
		_, _ = cw.Write(raw)
		cw.Flush()
		ac.resetIdle()
		return adapter.OutcomeSuccess, nil
	}

	buf := make([]byte, copyChunkBytes)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			events, serr := sp.push(buf[:n])
			for _, raw := range events {
				if out, ierr := step(raw); ierr != nil {
					return out, ierr
				}
				if werr := cw.Err(); werr != nil {
					return ac.clientFailed(werr)
				}
			}
			if serr != nil {
				if !committed {
					return ac.reclassifyStream(serr)
				}
				// Spec §6: past commit the recognizer's opinion no longer
				// matters. What is already in the carry still owes the client
				// those bytes; everything after is copied raw rather than
				// risked against the splitter a second time.
				if tail := sp.flush(); len(tail) > 0 {
					_, _ = cw.Write(tail)
				}
				recordWarning("scanner error after commit, forwarding raw: " + serr.Error())
				cw.Flush()
				// A cut here is not rendered as an error event: past the
				// overflow there is no event boundary to put one on.
				if _, err := copyFlushing(cw, resp.Body); err != nil {
					return ac.failedAfterCommit(err)
				}
				if werr := cw.Err(); werr != nil {
					return ac.clientFailed(werr)
				}
				return adapter.OutcomeSuccess, nil
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				if !committed {
					return ac.reclassifyStream(rerr)
				}
				// Classified before anything more is written, since a client
				// that is gone fails those writes as well.
				out, ierr := ac.failedAfterCommit(rerr)
				if out == adapter.OutcomeClientCancelled {
					return out, ierr
				}
				// Spec §9: after commit a failure becomes an in-stream error.
				// Whatever the splitter still holds goes out first, so the
				// error event lands on an event boundary rather than inside a
				// half-delivered one.
				if tail := sp.flush(); len(tail) > 0 {
					_, _ = cw.Write(tail)
				}
				recordWarning("upstream connection failed after commit: " + rerr.Error())
				se.WriteStreamError(cw, &ir.Error{Type: ir.ErrAPI, Message: msgUpstreamReadFailed})
				return out, ierr
			}
			break
		}
	}

	// A provider that ended without a trailing blank line still owes the
	// client those bytes.
	if tail := sp.flush(); len(tail) > 0 {
		if out, ierr := step(tail); ierr != nil {
			return out, ierr
		}
	}
	if !committed {
		// The stream ended with no content-bearing event. That is a
		// legitimately empty completion rather than a fault: failing over here
		// would burn the whole chain every time a model stops immediately.
		commit()
	}
	if werr := cw.Err(); werr != nil {
		return ac.clientFailed(werr)
	}
	if failed != nil {
		return ac.failedAfterCommit(failed)
	}
	return adapter.OutcomeSuccess, nil
}

// forwardedStreamError types an error event the recognizer flagged by reading
// it back through the adapter's own stream parser, which is where the
// provider's error vocabulary is mapped on the translated path. The two paths
// then classify the same event the same way. A payload the parser does not
// turn into an error stays untyped, which classifies as a provider fault.
func forwardedStreamError(fw adapter.Forwarder, raw []byte, maxLine int, payload string) error {
	if ad, ok := fw.(adapter.Adapter); ok {
		for _, err := range ad.ParseStream(bytes.NewReader(raw), maxLine) {
			if err != nil {
				return err
			}
		}
	}
	return errors.New(payload)
}

// errUnservableJSON is a 2xx body that is not JSON at all.
var errUnservableJSON = errors.New("upstream returned a 2xx body that is not valid JSON")

// unservableBody reports why a complete 2xx JSON body must not be forwarded
// as an answer: it is not JSON, or it is the provider's error envelope. The
// envelope is recognized by the same adapter code that finds an error event
// in a forwarded stream, whose payload is the same JSON object, and typed by
// the adapter's response parser where that parser types it. Forwarded, either
// is recorded as a success and resets the breaker for a provider that failed.
func unservableBody(fw adapter.Forwarder, resp *http.Response, body []byte) error {
	if !json.Valid(body) {
		return errUnservableJSON
	}
	if fw == nil || fw.RecognizeEvent(sse.Event{Data: string(body)}).ErrPayload == "" {
		return nil
	}
	if ad, ok := fw.(adapter.Adapter); ok {
		parsed := *resp
		parsed.Body = io.NopCloser(bytes.NewReader(body))
		var ie *ir.Error
		if _, err := ad.ParseResponse(&parsed); errors.As(err, &ie) {
			return ie
		}
	}
	return fmt.Errorf("upstream returned an error body under status %d", resp.StatusCode)
}

// streamErrorWriter renders a terminal in-stream error in the inbound
// dialect's wire form. passthroughOp satisfies it; the forwarder holds only
// this slice of it.
type streamErrorWriter interface {
	WriteStreamError(w http.ResponseWriter, e *ir.Error)
}

// forwardedResponseHeaders is the upstream header set a forwarded response may
// carry to the client. Everything else is dropped: a provider's Set-Cookie,
// CORS grant or server banner describes the provider's relationship with the
// gateway, not the gateway's with the client, and forwarding it would let an
// upstream set policy on Darkrouter's origin.
//
// Content-Length is deliberately absent even though it looks harmless. Spec
// §8: stripping a usage chunk changes the length even when nothing else does,
// and a wrong length is worse than none.
var forwardedResponseHeaders = map[string]bool{
	"content-type":  true,
	"cache-control": true,
	"x-request-id":  true,
}

// copyResponseHeaders forwards the allowlisted upstream headers plus every
// x-ratelimit-* header, which clients use to pace themselves against the
// provider's quota. Darkrouter's own diagnostics are added after this call,
// so an upstream echoing one cannot spoof it.
func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		lk := strings.ToLower(k)
		if !forwardedResponseHeaders[lk] && !strings.HasPrefix(lk, "x-ratelimit-") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// maxForwardedUnaryBytes bounds what one unary response buffers for usage
// extraction. A body past it is still forwarded in full — breaching the cap
// costs the token count, never the response.
//
// Buffering the whole body rather than spec §7's bounded tail is deliberate:
// the IR path's ParseResponse already reads the entire unary body into memory,
// so this changes no memory characteristic of the product, and there is no
// truncation point to get wrong.
//
// A var rather than a const so a test can shrink it: the alternative is
// allocating tens of megabytes on every run of the suite.
var maxForwardedUnaryBytes int64 = 32 << 20

func (e *Executor) forwardUnary(cw *CommitWriter, resp *http.Response, ac *AttemptCtx,
	fw adapter.Forwarder) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()
	rec, c := ac.Rec, ac.Cand
	ac.resetIdle()

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxForwardedUnaryBytes+1))
	oversize := int64(len(body)) > maxForwardedUnaryBytes
	if rerr != nil && !oversize {
		// Nothing has reached the client, so this is still a failover, and
		// a 200 that cannot be read counts against the provider like a 5xx.
		return failedParse(ac, resp, fmt.Errorf("%s: %w", msgUpstreamReadFailed, rerr))
	}

	if !oversize {
		if err := unservableBody(fw, resp, body); err != nil {
			return failedParse(ac, resp, err)
		}
	}

	warns := ac.Warns
	if oversize {
		warns = append(warns, ir.Warning{
			Field: "usage", Target: c.ProviderID + "/" + c.Model,
			Reason: "the response exceeded the buffer for usage extraction; tokens are unknown",
		})
	} else if u := fw.RecognizeUsage(body); u != nil {
		applyUsage(rec, u)
	} else {
		warns = append(warns, ir.Warning{
			Field: "usage", Target: c.ProviderID + "/" + c.Model,
			Reason: "the response carried no usage; tokens are recorded as unknown",
		})
	}

	ac.served(warns)
	copyResponseHeaders(cw.Header(), resp.Header)
	e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
	cw.WriteHeader(resp.StatusCode)
	_, _ = cw.Write(body)
	if oversize {
		// Committed already: a truncated body would be worse than a slow one.
		if _, err := copyFlushing(cw, resp.Body); err != nil {
			return ac.failedAfterCommit(err)
		}
	}
	return adapter.OutcomeSuccess, nil
}
