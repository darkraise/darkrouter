package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// APIKeyInvalid reports whether body is a Google error whose ErrorInfo
// reason is API_KEY_INVALID: the key is malformed, unknown or expired. Google
// sends it on a 400, so the status alone never names the refusal.
func APIKeyInvalid(body []byte) bool {
	var e struct {
		Error struct {
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return false
	}
	for _, d := range e.Error.Details {
		if d.Type == "type.googleapis.com/google.rpc.ErrorInfo" && d.Reason == "API_KEY_INVALID" {
			return true
		}
	}
	return false
}

// ClassifyBody refines a 400. Google refuses a dead API key with a 400 whose
// only mark is the ErrorInfo reason; read as a bad request it would end
// failover at the first key and never cool the one that is dead.
func (a *Adapter) ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	base := Classify(resp, err)
	if base == adapter.OutcomeFatal && resp != nil && resp.StatusCode == http.StatusBadRequest &&
		APIKeyInvalid(body) {
		return adapter.OutcomeRetryableCredential
	}
	return base
}

var _ adapter.BodyClassifier = (*Adapter)(nil)
