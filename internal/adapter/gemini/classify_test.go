package gemini

import (
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// Google's answer to an unknown or expired API key, as the Gemini API sends it.
const apiKeyInvalidBody = `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com","metadata":{"service":"generativelanguage.googleapis.com"}}]}}`

// A dead key is the credential's failure, not the request's. Read as a bad
// request it would end failover at the first key instead of trying the next,
// and the dead key would never cool.
func TestADeadAPIKeyIsACredentialFailure(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	if got := New().ClassifyBody(resp, []byte(apiKeyInvalidBody), nil); got != adapter.OutcomeRetryableCredential {
		t.Errorf("outcome = %v, want retryable credential", got)
	}
}

func TestAnOrdinaryBadRequestStaysFatal(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest}
	body := `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`
	if got := New().ClassifyBody(resp, []byte(body), nil); got != adapter.OutcomeFatal {
		t.Errorf("outcome = %v, want fatal", got)
	}
}

func TestAPIKeyInvalidReadsOnlyTheErrorInfoReason(t *testing.T) {
	if !APIKeyInvalid([]byte(apiKeyInvalidBody)) {
		t.Error("the documented refusal was not recognised")
	}
	for _, body := range []string{
		`{"error":{"message":"API_KEY_INVALID"}}`,
		`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_SERVICE_BLOCKED"}]}}`,
		`not json`,
	} {
		if APIKeyInvalid([]byte(body)) {
			t.Errorf("%s read as a dead key", body)
		}
	}
}
