package catalog

import "encoding/json"

// GoogleAPIKeyInvalid reports whether body is a Google error whose ErrorInfo
// reason is API_KEY_INVALID: the key is malformed, unknown or expired. Google
// sends it on a 400, so the status alone never names the refusal.
func GoogleAPIKeyInvalid(body []byte) bool {
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
