package adapter

import (
	"errors"
	"net/http"
	"testing"
)

func TestClassifyStatusBuckets(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{200, OutcomeSuccess},
		{204, OutcomeSuccess},
		{302, OutcomeRetryableProvider},
		{400, OutcomeFatal},
		{401, OutcomeRetryableCredential},
		{402, OutcomeRetryableCredential},
		{403, OutcomeRetryableCredential},
		{404, OutcomeRetryableModel},
		{408, OutcomeRetryableProvider},
		{413, OutcomeFatal},
		{422, OutcomeFatal},
		{429, OutcomeRetryableProvider},
		{500, OutcomeRetryableProvider},
		{503, OutcomeRetryableProvider},
		{529, OutcomeRetryableProvider},
	}
	for _, tc := range cases {
		got := ClassifyStatus(&http.Response{StatusCode: tc.code}, nil)
		if got != tc.want {
			t.Errorf("ClassifyStatus(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestClassifyStatusTreatsTransportErrorsAsRetryable(t *testing.T) {
	if got := ClassifyStatus(nil, errors.New("dial tcp: no such host")); got != OutcomeRetryableProvider {
		t.Errorf("transport error = %q", got)
	}
	if got := ClassifyStatus(nil, nil); got != OutcomeRetryableProvider {
		t.Errorf("no response and no error = %q; there is nothing to succeed with", got)
	}
}
