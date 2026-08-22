package adapter

import "net/http"

// ClassifyStatus buckets an upstream result by its status line. Master design
// §8.1 is authoritative, and the default buckets matter as much as the listed
// codes: without them, DNS failures and TLS errors get classified differently
// by different adapters.
//
// It is dialect-independent on purpose. Anthropic's 529 and Gemini's 503 both
// land on the ≥500 rule without either adapter naming them.
func ClassifyStatus(resp *http.Response, err error) Outcome {
	if err != nil || resp == nil {
		return OutcomeRetryableProvider
	}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return OutcomeSuccess
	case code == 401, code == 402, code == 403:
		return OutcomeRetryableCredential
	case code == 404:
		return OutcomeRetryableModel
	case code == 408, code == 429:
		return OutcomeRetryableProvider
	case code >= 300 && code < 400:
		// Redirects are never followed: Go's client converts a redirected POST
		// into a body-less GET.
		return OutcomeRetryableProvider
	case code >= 500:
		return OutcomeRetryableProvider
	default:
		return OutcomeFatal
	}
}
