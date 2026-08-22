package openai

import (
	"net/http/httptest"
	"testing"
)

func TestProxyTokenReadsTheBearerHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer", "Bearer sk-abc", "sk-abc"},
		{"lowercase scheme is still a bearer", "bearer sk-abc", "sk-abc"},
		{"surrounding space is trimmed", "Bearer   sk-abc  ", "sk-abc"},
		{"no header", "", ""},
		{"wrong scheme", "Basic sk-abc", ""},
		{"scheme only", "Bearer", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if got := New().ProxyToken(r); got != tc.want {
				t.Errorf("ProxyToken = %q, want %q", got, tc.want)
			}
		})
	}
}
