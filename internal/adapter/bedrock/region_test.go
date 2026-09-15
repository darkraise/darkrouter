package bedrock

import (
	"context"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// The region becomes part of the hostname a SigV4 signature is sent to. A row
// written before the admin API validated it still reaches the builder and the
// lister, so both refuse one that would move the request to another host.
var hostMovingRegions = []string{
	"evil.example/", "evil.example#", "x@evil.example", "us-east-1.evil.example", "US-EAST-1",
}

func TestARegionThatWouldMoveTheHostIsRefused(t *testing.T) {
	for _, region := range hostMovingRegions {
		t.Run(region, func(t *testing.T) {
			if _, _, err := New().BuildRequest(context.Background(),
				&adapter.Target{Region: region, Model: "m"}, simple()); err == nil {
				t.Errorf("built a runtime request for region %q", region)
			}

			p := listerProbe("")
			p.Region = region
			sent := false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				sent = true
				return nil, errStop
			})}
			if _, err := NewLister(client).List(context.Background(), p); err == nil || sent {
				t.Errorf("listed for region %q: err = %v, sent = %v", region, err, sent)
			}
		})
	}
}

func TestDocumentedRegionsAreAccepted(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-central-2", "ap-southeast-5", "us-gov-west-1"} {
		if _, _, err := New().BuildRequest(context.Background(),
			&adapter.Target{Region: region, Model: "m"}, simple()); err != nil {
			t.Errorf("region %q: %v", region, err)
		}
	}
}
