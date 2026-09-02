package adapter

import "testing"

func TestEscapePathSegmentMatchesTheAWSRule(t *testing.T) {
	// url.PathEscape leaves ':' alone, which is legal in a path segment and is
	// not what AWS signs. Every inference-profile id contains one, so this is
	// the difference between working and a 403 on every request.
	for in, want := range map[string]string{
		"anthropic.claude-3-5-sonnet-20241022-v2:0": "anthropic.claude-3-5-sonnet-20241022-v2%3A0",
		"us.anthropic.claude-x-v1:0":                "us.anthropic.claude-x-v1%3A0",
		"plain-model_1.0~x":                         "plain-model_1.0~x",
		"a/b":                                       "a%2Fb",
	} {
		if got := EscapePathSegment(in); got != want {
			t.Errorf("EscapePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
