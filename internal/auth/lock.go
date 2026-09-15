package auth

import "context"

// waitMutex is a mutex whose waiters can stop waiting when their own context
// ends. A token exchange can stall for as long as tokenTimeout, and a caller
// queued behind one must still be able to meet its deadline.
type waitMutex chan struct{}

func newWaitMutex() waitMutex { return make(waitMutex, 1) }

func (l waitMutex) lock(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l waitMutex) unlock() { <-l }

// detach runs work to completion on a context the caller cannot cancel,
// bounded by tokenTimeout, and waits for it only as long as ctx allows.
//
// A token endpoint can act on a request before its answer arrives — rotating a
// refresh token is exactly that — so abandoning the exchange when a client
// hangs up throws away the only pair that still works. The work carries on and
// records its result for the next caller instead.
func detach(ctx context.Context, work func(context.Context) (string, error)) (string, error) {
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tokenTimeout)
		defer cancel()
		v, err := work(wctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
