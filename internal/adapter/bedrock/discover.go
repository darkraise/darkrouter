package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// ControlPlaneFor is the management endpoint, which is a different host from
// the runtime one. A ListFoundationModels sent to bedrock-runtime is a 404 that
// reads like a missing model.
func ControlPlaneFor(region string) string {
	return "https://bedrock." + region + ".amazonaws.com"
}

type Lister struct{ client *http.Client }

func NewLister(c *http.Client) *Lister {
	if c == nil {
		// Matches discovery's default probe timeout. http.DefaultClient has
		// none, and a control plane that accepts a request and never answers
		// would otherwise hold its caller forever.
		c = &http.Client{Timeout: 15 * time.Second}
	}
	return &Lister{client: c}
}

type modelSummary struct {
	ModelID                 string   `json:"modelId"`
	ModelName               string   `json:"modelName"`
	InferenceTypesSupported []string `json:"inferenceTypesSupported"`
	ModelLifecycle          struct {
		Status string `json:"status"`
	} `json:"modelLifecycle"`
	OutputModalities []string `json:"outputModalities"`
}

type profileSummary struct {
	InferenceProfileID   string `json:"inferenceProfileId"`
	InferenceProfileName string `json:"inferenceProfileName"`
	Status               string `json:"status"`
	Models               []struct {
		ModelArn string `json:"modelArn"`
	} `json:"models"`
}

// List catalogues what can actually be invoked.
//
// Spec §3.3: ListFoundationModels returns bare model ids, many of which are not
// on-demand invocable, and the invocable identifiers come from
// ListInferenceProfiles. Cataloguing only the first call's output would store
// precisely the identifiers that fail.
func (l *Lister) List(ctx context.Context, p catalog.Probe) ([]catalog.Discovered, error) {
	if p.Authorize == nil {
		return nil, errors.New("bedrock discovery needs a signed request; no authorizer was supplied")
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" || strings.Contains(base, "bedrock-runtime") {
		if p.Region == "" {
			return nil, errors.New("bedrock discovery needs a region")
		}
		if err := CheckRegion(p.Region); err != nil {
			return nil, err
		}
		base = ControlPlaneFor(p.Region)
	}

	var models struct {
		ModelSummaries []modelSummary `json:"modelSummaries"`
	}
	if err := l.get(ctx, p, base+"/foundation-models", &models); err != nil {
		return nil, err
	}
	summaries, err := l.profiles(ctx, p, base)
	if err != nil {
		return nil, err
	}

	// covered is every bare model id reachable through a profile. Those are
	// catalogued under the profile id instead of their own: routing to the bare
	// id returns a 400 telling the operator to use a profile, which is a worse
	// error than the model simply not being offered.
	covered := map[string]bool{}
	out := make([]catalog.Discovered, 0, len(summaries)+len(models.ModelSummaries))
	for _, pr := range summaries {
		if pr.Status != "" && pr.Status != "ACTIVE" {
			continue
		}
		for _, m := range pr.Models {
			covered[modelIDFromARN(m.ModelArn)] = true
		}
		out = append(out, catalog.Discovered{ModelID: pr.InferenceProfileID})
	}

	for _, m := range models.ModelSummaries {
		if covered[m.ModelID] {
			continue
		}
		if m.ModelLifecycle.Status != "" && m.ModelLifecycle.Status != "ACTIVE" {
			continue
		}
		if !supports(m.InferenceTypesSupported, "ON_DEMAND") {
			// PROVISIONED-only and INFERENCE_PROFILE-only models cannot be
			// invoked by id. Cataloguing them stores identifiers that 400.
			continue
		}
		out = append(out, catalog.Discovered{ModelID: m.ModelID})
	}
	return out, nil
}

// profiles reads every page of ListInferenceProfiles. ListFoundationModels has
// no pagination; this call does, and stopping at the first page drops every
// profile past it from the catalog.
func (l *Lister) profiles(ctx context.Context, p catalog.Probe, base string) ([]profileSummary, error) {
	var (
		out  []profileSummary
		seen = map[string]bool{}
		next string
	)
	for {
		q := url.Values{"maxResults": {"1000"}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var page struct {
			Summaries []profileSummary `json:"inferenceProfileSummaries"`
			NextToken string           `json:"nextToken"`
		}
		if err := l.get(ctx, p, base+"/inference-profiles?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Summaries...)
		if page.NextToken == "" {
			return out, nil
		}
		if seen[page.NextToken] {
			return nil, errors.New("bedrock inference-profile listing repeated a page token")
		}
		seen[page.NextToken] = true
		next = page.NextToken
	}
}

func (l *Lister) get(ctx context.Context, p catalog.Probe, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if err := p.Authorize(ctx, req); err != nil {
		return fmt.Errorf("sign %s: %w", url, err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newListError(url, resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// ListError is a control-plane refusal. Type is the AWS error type, which is
// what separates a key AWS does not recognise from a policy that denies it:
// both arrive as a 403.
type ListError struct {
	URL        string
	Status     string
	StatusCode int
	Type       string
	Message    string
}

func (e *ListError) Error() string {
	msg := fmt.Sprintf("%s returned %s", e.URL, e.Status)
	if e.Type != "" {
		msg += ": " + e.Type
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

func newListError(url string, resp *http.Response) *ListError {
	e := &ListError{URL: url, Status: resp.Status, StatusCode: resp.StatusCode}
	var body struct {
		Type     string `json:"__type"`
		Message  string `json:"message"`
		MessageU string `json:"Message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	// restJson1 names the type in X-Amzn-Errortype, optionally followed by
	// ":" and a namespace URI; __type may carry a "namespace#" prefix.
	e.Type = resp.Header.Get("X-Amzn-Errortype")
	if e.Type == "" {
		e.Type = body.Type
	}
	if i := strings.Index(e.Type, ":"); i >= 0 {
		e.Type = e.Type[:i]
	}
	if i := strings.LastIndex(e.Type, "#"); i >= 0 {
		e.Type = e.Type[i+1:]
	}
	e.Message = body.Message
	if e.Message == "" {
		e.Message = body.MessageU
	}
	return e
}

// modelIDFromARN takes the identifier off the end of a foundation-model ARN.
// The ARN is what a profile names its members by; the catalog keys on ids.
func modelIDFromARN(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func supports(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}
