package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkrouter/internal/crypto"
)

// RotateMasterKey re-encrypts every credential and rewrites the verifier under
// a key derived from newMaster with a fresh salt.
//
// A fresh salt matters: reusing the old one would leave two databases derived
// from related material, and the salt costs nothing to regenerate.
func RotateMasterKey(ctx context.Context, d *DB, oldKey *crypto.Key, newMaster string) error {
	return rotateWithHook(ctx, d, oldKey, newMaster, nil)
}

// rotateWithHook is RotateMasterKey with an injection point after each
// credential is re-sealed, so a test can prove that an interruption rolls back.
func rotateWithHook(ctx context.Context, d *DB, oldKey *crypto.Key, newMaster string,
	afterEach func(i int) error) error {

	if newMaster == "" {
		return errors.New("the new master key is empty")
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return err
	}
	newKey, err := crypto.DeriveKey(newMaster, salt, crypto.DefaultIterations)
	if err != nil {
		return err
	}

	// The FULL-sync handle is required here. Without it, a power loss after the
	// commit but before the WAL syncs rolls the rotation back while the operator
	// has already changed DARKROUTER_MASTER_KEY, and the next startup fails the
	// verifier with no way to tell why.
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := allCredentialRows(ctx, tx)
	if err != nil {
		return err
	}
	for i, r := range rows {
		plaintext, err := oldKey.Open(r.ciphertext, r.nonce, []byte(r.id))
		if err != nil {
			return fmt.Errorf("credential %s could not be decrypted with the current key; "+
				"rotation aborted and nothing was changed: %w", r.id, err)
		}
		ciphertext, nonce, err := newKey.Seal(plaintext, []byte(r.id))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE provider_keys SET ciphertext = ?, nonce = ? WHERE id = ?`,
			ciphertext, nonce, r.id); err != nil {
			return fmt.Errorf("re-encrypt credential %s: %w", r.id, err)
		}
		if afterEach != nil {
			if err := afterEach(i); err != nil {
				return err
			}
		}
	}

	verifierCT, verifierNonce, err := newKey.Seal([]byte(verifierPlaintext), []byte(verifierAAD))
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{settingKDFSalt, hex.EncodeToString(salt)},
		{settingKDFIterations, strconv.Itoa(crypto.DefaultIterations)},
		{settingKDFVerifierCiphertext, hex.EncodeToString(verifierCT)},
		{settingKDFVerifierNonce, hex.EncodeToString(verifierNonce)},
	} {
		if err := putSetting(ctx, tx, kv[0], kv[1]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rotation: %w", err)
	}
	return nil
}
