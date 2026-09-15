package vertex

import (
	"fmt"
	"regexp"
)

var (
	// A location is one hostname label: us-central1, europe-west4, global.
	// Anything else, a dot, slash or @ above all, would put the token on
	// another host.
	locationShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	// A project ID, a project number, or a legacy domain-scoped ID
	// (example.com:my-project). It is one path segment, so it may carry
	// nothing that ends or escapes one.
	projectShape = regexp.MustCompile(`^[a-z0-9][a-z0-9.:-]{0,99}$`)
)

// CheckEndpoint refuses a project or location that would not form the
// documented endpoint.
func CheckEndpoint(project, location string) error {
	if project == "" || location == "" {
		return fmt.Errorf("vertex target needs a project and a location")
	}
	if !locationShape.MatchString(location) {
		return fmt.Errorf("vertex location %q is not a location name such as us-central1 or global", location)
	}
	if !projectShape.MatchString(project) {
		return fmt.Errorf("vertex project %q is not a project ID or number", project)
	}
	return nil
}
