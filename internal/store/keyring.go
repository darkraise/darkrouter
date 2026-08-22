package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkrouter/internal/crypto"
)

const (
	settingKDFSalt               = "kdf_salt"
	settingKDFIterations         = "kdf_iterations"
	settingKDFVerifierCiphertext = "kdf_verifier_ciphertext"
	settingKDFVerifierNonce      = "kdf_verifier_nonce"
)

// verifierPlaintext is a fixed known value. Encrypting it under the derived key
// and checking it at startup is what turns a wrong master key into a clear
// failure instead of credentials that decrypt into garbage at request time.
const verifierPlaintext = "darkrouter-kdf-verifier-v1"

// verifierAAD binds the verifier to its purpose, so its ciphertext cannot be
// pasted into a credential row.
const verifierAAD = "kdf-verifier"

// queryer is satisfied by both *sql.DB and *sql.Tx, so settings can be read
// inside or outside a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func getSetting(ctx context.Context, q queryer, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %q: %w", key, err)
	}
	return v, true, nil
}

func putSetting(ctx context.Context, e execer, key, value string) error {
	_, err := e.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// OpenKeyring derives the credential-encryption key. On first run it generates
// a salt, records the iteration count, and stores a verifier. On every later
// run it re-derives from the stored parameters and checks the verifier.
func OpenKeyring(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	if master == "" {
		return nil, errors.New(
			"DARKROUTER_MASTER_KEY is not set; it is required from phase 2 onward " +
				"because provider credentials are encrypted at rest")
	}

	saltHex, ok, err := getSetting(ctx, d.Read, settingKDFSalt)
	if err != nil {
		return nil, err
	}
	if !ok {
		return initKeyring(ctx, d, master)
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, fmt.Errorf("stored kdf salt is not valid hex: %w", err)
	}
	itersRaw, ok, err := getSetting(ctx, d.Read, settingKDFIterations)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New(
			"the kdf salt is present but the iteration count is missing; " +
				"this database's keyring is incomplete and cannot be derived from")
	}
	iterations, err := strconv.Atoi(itersRaw)
	if err != nil {
		return nil, fmt.Errorf("stored kdf iteration count is not a number: %w", err)
	}

	key, err := crypto.DeriveKey(master, salt, iterations)
	if err != nil {
		return nil, err
	}
	if err := checkVerifier(ctx, d, key); err != nil {
		return nil, err
	}
	return key, nil
}

func checkVerifier(ctx context.Context, d *DB, key *crypto.Key) error {
	ctHex, okCT, err := getSetting(ctx, d.Read, settingKDFVerifierCiphertext)
	if err != nil {
		return err
	}
	nonceHex, okNonce, err := getSetting(ctx, d.Read, settingKDFVerifierNonce)
	if err != nil {
		return err
	}
	if !okCT || !okNonce {
		return errors.New(
			"the kdf verifier is missing; this database's keyring is incomplete, " +
				"so a wrong DARKROUTER_MASTER_KEY could not be detected")
	}
	ciphertext, err := hex.DecodeString(ctHex)
	if err != nil {
		return fmt.Errorf("stored kdf verifier is not valid hex: %w", err)
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return fmt.Errorf("stored kdf verifier nonce is not valid hex: %w", err)
	}

	plaintext, err := key.Open(ciphertext, nonce, []byte(verifierAAD))
	if err != nil || string(plaintext) != verifierPlaintext {
		return errors.New(
			"DARKROUTER_MASTER_KEY does not match this database; " +
				"every stored credential was encrypted under a different key. " +
				"Restore the original value, or run 'darkrouter rotate-key' with the old one")
	}
	return nil
}

// initKeyring runs on first use. It writes the salt, the iteration count, and
// the verifier in one transaction on the FULL-sync handle: a half-written
// keyring would be indistinguishable from a wrong master key on the next start.
func initKeyring(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	salt, err := crypto.NewSalt()
	if err != nil {
		return nil, err
	}
	key, err := crypto.DeriveKey(master, salt, crypto.DefaultIterations)
	if err != nil {
		return nil, err
	}
	ciphertext, nonce, err := key.Seal([]byte(verifierPlaintext), []byte(verifierAAD))
	if err != nil {
		return nil, err
	}

	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin keyring init: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, kv := range [][2]string{
		{settingKDFSalt, hex.EncodeToString(salt)},
		{settingKDFIterations, strconv.Itoa(crypto.DefaultIterations)},
		{settingKDFVerifierCiphertext, hex.EncodeToString(ciphertext)},
		{settingKDFVerifierNonce, hex.EncodeToString(nonce)},
	} {
		if err := putSetting(ctx, tx, kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit keyring init: %w", err)
	}
	return key, nil
}
