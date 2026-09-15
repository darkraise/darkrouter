package vertex

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// The location becomes part of the hostname the service-account token is sent
// to, and the project part of its path. A row written before the admin API
// validated them still reaches the builder, so the builder refuses one that
// would move the request somewhere else.
func TestATargetThatWouldMoveTheEndpointIsRefused(t *testing.T) {
	cases := map[string]struct{ project, location string }{
		"a location with a path":     {"proj", "evil.example/"},
		"a location with a dot":      {"proj", "evil.example"},
		"a location with userinfo":   {"proj", "x@evil.example"},
		"a location with a fragment": {"proj", "evil#"},
		"an uppercase location":      {"proj", "US-CENTRAL1"},
		"a project with a slash":     {"proj/../../other", "us-central1"},
		"a project with a query":     {"proj?x=1", "us-central1"},
		"a project with a fragment":  {"proj#x", "us-central1"},
	}
	builders := map[string]func(*adapter.Target) error{
		"google": func(tgt *adapter.Target) error {
			_, _, err := New().BuildRequest(context.Background(), tgt, req())
			return err
		},
		"anthropic": func(tgt *adapter.Target) error {
			tgt.Publisher = PublisherAnthropic
			_, _, err := New().BuildRequest(context.Background(), tgt, req())
			return err
		},
		"embedding": func(tgt *adapter.Target) error {
			_, _, err := New().BuildEmbedding(context.Background(), tgt,
				&ir.EmbeddingRequest{Model: "text-embedding-005", Input: []string{"a"}})
			return err
		},
	}
	for name, tc := range cases {
		for builder, build := range builders {
			t.Run(name+"/"+builder, func(t *testing.T) {
				tgt := googleTarget()
				tgt.Project, tgt.Location = tc.project, tc.location
				if build(tgt) == nil {
					t.Errorf("built a request for project %q, location %q", tc.project, tc.location)
				}
			})
		}
	}
}

func TestDocumentedLocationsAndProjectsAreAccepted(t *testing.T) {
	for _, loc := range []string{"us-central1", "europe-west4", "asia-northeast1", "us-east5", "global"} {
		tgt := googleTarget()
		tgt.Location = loc
		if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err != nil {
			t.Errorf("location %q: %v", loc, err)
		}
	}
	for _, project := range []string{"my-project-123", "123456789012", "example.com:my-project"} {
		tgt := googleTarget()
		tgt.Project = project
		if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err != nil {
			t.Errorf("project %q: %v", project, err)
		}
	}
}
