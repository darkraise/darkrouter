package exec

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
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
	fw adapter.Forwarder, strip bool) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()

	cfg, rec, c := ac.Cfg, ac.Rec, ac.Cand
	maxLine := cfg.Server.SSE.MaxLineBytes
	sp := &eventSplitter{max: maxLine}

	var (
		pending      [][]byte
		pendingBytes int
		committed    bool
		usage        ir.Usage
	)

	// Post-commit, policy.timeout.total stops applying and policy.timeout.idle
	// bounds the gap between events instead — the same switch the IR stream
	// path makes, for the same reason.
	resetIdle := func() {
		if d := cfg.Policy.Timeout.Idle; d > 0 {
			ac.Timer.Reset(d)
		}
	}

	commit := func() {
		committed = true
		ttft := time.Since(rec.TS).Milliseconds()
		rec.TTFTMs = &ttft
		rec.FinalProviderID = c.ProviderID
		rec.FinalModel = c.Model
		rec.Warnings = warningStrings(ac.Warns)
		copyResponseHeaders(cw.Header(), resp.Header)
		e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
		cw.WriteHeader(resp.StatusCode)
		for _, raw := range pending {
			_, _ = cw.Write(raw)
		}
		pending, pendingBytes = nil, 0
		cw.Flush()
		resetIdle()
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
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, re.ErrPayload)
			}
			if re.Content {
				commit()
				_, _ = cw.Write(raw)
				cw.Flush()
				return adapter.OutcomeSuccess, nil
			}
			if strip && re.UsageOnly {
				return adapter.OutcomeSuccess, nil
			}
			if cap := cfg.Server.SSE.MaxPrecommitBytes; cap > 0 && pendingBytes+len(raw) > cap {
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, ErrPreCommitBufferFull.Error())
			}
			pendingBytes += len(raw)
			pending = append(pending, raw)
			return adapter.OutcomeSuccess, nil
		}

		if strip && re.UsageOnly {
			return adapter.OutcomeSuccess, nil
		}
		_, _ = cw.Write(raw)
		cw.Flush()
		resetIdle()
		return adapter.OutcomeSuccess, nil
	}

	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			events, serr := sp.push(buf[:n])
			for _, raw := range events {
				if out, ierr := step(raw); ierr != nil {
					return out, ierr
				}
			}
			if serr != nil {
				if !committed {
					return adapter.OutcomeRetryableProvider,
						e.reclassifyStream(c, resp, rec, serr.Error())
				}
				// The client already has bytes; the stream ends here.
				return adapter.OutcomeSuccess, nil
			}
		}
		if rerr != nil {
			if rerr != io.EOF && !committed {
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, rerr.Error())
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
	return adapter.OutcomeSuccess, nil
}

// hopByHop is RFC 9110 §7.6.1's connection-specific header set.
var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true,
}

// copyResponseHeaders forwards the upstream's headers minus the ones that
// describe the connection or the encoding.
//
// Content-Encoding and Content-Length are dropped rather than copied. Spec §8:
// copying them through would label bytes with an encoding or a length that the
// forward no longer matches — and stripping a usage chunk changes the length
// even when nothing else does. Darkrouter's own diagnostics are added after
// this call, so an upstream echoing one cannot spoof it.
func copyResponseHeaders(dst, src http.Header) {
	skip := map[string]bool{"content-length": true, "content-encoding": true}
	for _, v := range src.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			skip[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for k, vs := range src {
		lk := strings.ToLower(k)
		if hopByHop[lk] || skip[lk] || strings.HasPrefix(lk, "x-darkrouter-") {
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
const maxForwardedUnaryBytes = 32 << 20

func (e *Executor) forwardUnary(cw *CommitWriter, resp *http.Response, ac *AttemptCtx,
	fw adapter.Forwarder) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()
	rec, c := ac.Rec, ac.Cand

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxForwardedUnaryBytes+1))
	oversize := int64(len(body)) > maxForwardedUnaryBytes
	if rerr != nil && !oversize {
		// Nothing has reached the client, so this is still a failover.
		return adapter.OutcomeRetryableProvider, &ir.Error{Type: ir.ErrAPI, Message: rerr.Error()}
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

	ttft := time.Since(rec.TS).Milliseconds()
	rec.TTFTMs = &ttft
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	rec.Warnings = warningStrings(warns)

	copyResponseHeaders(cw.Header(), resp.Header)
	e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
	cw.WriteHeader(resp.StatusCode)
	_, _ = cw.Write(body)
	if oversize {
		// Committed already: a truncated body would be worse than a slow one.
		_, _ = io.Copy(cw, resp.Body)
	}
	return adapter.OutcomeSuccess, nil
}
