package vertex

import "testing"

// Vertex has three host shapes. The multi-regions answer only on their
// representative hosts, and newer Gemini models are served on global and the
// multi-regions alone, so a wrong host there is the only way to reach them
// failing.
func TestEndpointForEachLocationShape(t *testing.T) {
	cases := map[string]string{
		"us-central1": "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1",
		"global":      "https://aiplatform.googleapis.com/v1/projects/p/locations/global",
		"us":          "https://aiplatform.us.rep.googleapis.com/v1/projects/p/locations/us",
		"eu":          "https://aiplatform.eu.rep.googleapis.com/v1/projects/p/locations/eu",
	}
	for location, want := range cases {
		if got := EndpointFor("p", location); got != want {
			t.Errorf("EndpointFor(%q) = %s, want %s", location, got, want)
		}
	}
}
