package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// emptyPayloadHash is the SHA-256 of the empty string, which SignHTTP requires
// even for a request with no body.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// signingService is the SigV4 service name. The runtime and control-plane
// endpoints are different hosts sharing one signing name.
const signingService = "bedrock"

// AWSCredentials is the explicit key pair a provider row may carry. Spec §3.2
// prefers it when given and falls back to the standard chain otherwise.
type AWSCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

func ParseAWSCredentials(raw []byte) (AWSCredentials, error) {
	var c AWSCredentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return AWSCredentials{}, fmt.Errorf("parse aws credentials: %w", err)
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return AWSCredentials{}, errors.New("aws credentials need an access key id and a secret access key")
	}
	return c, nil
}

// signerFor returns an authorizer that signs with fixed credentials.
//
// now is injected so the known-answer vectors can pin a timestamp. Everything
// else about the signature is derived from the request, which is the property
// the vectors exist to protect.
func signerFor(creds AWSCredentials, service, region string, now func() time.Time) Authorizer {
	signer := v4.NewSigner()
	awsCreds := aws.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}
	return func(ctx context.Context, r *http.Request) error {
		hash, err := payloadHash(r)
		if err != nil {
			return err
		}
		return signer.SignHTTP(ctx, awsCreds, r, hash, service, region, now().UTC())
	}
}

// payloadHash hashes the body and puts it back. The body is already
// materialized by the time an authorizer runs, so this reads a bytes.Reader
// rather than a network stream — but restoring it is not optional: signing
// hashes the payload and sending it requires the same bytes again.
func payloadHash(r *http.Request) (string, error) {
	if r.Body == nil {
		return emptyPayloadHash, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("read body for signing: %w", err)
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// sigv4 resolves the credential and returns its authorizer.
func (m *Manager) sigv4(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if t.Region == "" {
		return nil, fmt.Errorf("provider %q uses sigv4 but declares no region", t.ProviderID)
	}
	if c.Secret != "" {
		creds, err := ParseAWSCredentials([]byte(c.Secret))
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", t.ProviderID, err)
		}
		return signerFor(creds, signingService, t.Region, time.Now), nil
	}
	// No explicit credential: environment, shared config, or instance role.
	// Retrieved through the chain on every call rather than cached once,
	// because an instance-role credential expires and the chain renews it.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(t.Region))
	if err != nil {
		return nil, fmt.Errorf("provider %q: aws credential chain: %w", t.ProviderID, err)
	}
	signer := v4.NewSigner()
	return func(ctx context.Context, r *http.Request) error {
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return fmt.Errorf("retrieve aws credentials: %w", err)
		}
		hash, err := payloadHash(r)
		if err != nil {
			return err
		}
		return signer.SignHTTP(ctx, creds, r, hash, signingService, t.Region, time.Now().UTC())
	}, nil
}
