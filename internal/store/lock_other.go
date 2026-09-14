//go:build !unix

package store

// Lock has no advisory lock to take on this platform, so neither the gateway
// nor rotate-key can tell that the other is running: stop the gateway before
// rotating the key.
func Lock(string) (unlock func() error, err error) {
	return func() error { return nil }, nil
}
