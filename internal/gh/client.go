package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	ghapi "github.com/cli/go-gh/v2/pkg/api"
	"github.com/madicen/appr-ai-sal/internal/applog"
)

// This file is the in-process transport seam for the gh layer (R6.2). All
// REST and GraphQL traffic runs through go-gh's clients, which resolve the
// SAME auth + host as the `gh` CLI — so we keep the "no separate auth surface"
// design while getting real HTTP status codes (fed into the APIError taxonomy
// in errors.go) and native Retry-After handling. The sugar commands
// (`gh pr view` / `gh pr diff` / `gh pr list`) deliberately stay as shell-outs
// for now (see run/runJSON in gh.go); the plan stages that migration later.
//
// Testability: nothing here spawns a process, and every call goes through the
// injectable client constructors below. Tests install a fake HTTP transport
// (stubGHTransport) so go-gh's real parsing runs against canned responses with
// no network, keeping the suite hermetic.

// restDoer is the subset of go-gh's *api.RESTClient this package depends on.
// Declaring it as an interface lets a test swap in a mock when exercising the
// error-mapping paths directly.
type restDoer interface {
	DoWithContext(ctx context.Context, method, path string, body io.Reader, response interface{}) error
	RequestWithContext(ctx context.Context, method, path string, body io.Reader) (*http.Response, error)
}

// graphQLDoer is the subset of go-gh's *api.GraphQLClient this package needs.
type graphQLDoer interface {
	DoWithContext(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error
}

// ghClientTransport, when non-nil, overrides the HTTP transport used by the
// go-gh clients. Production leaves it nil so go-gh resolves the same auth +
// host as the gh CLI. Tests set it (via stubGHTransport) to a fake
// RoundTripper — or an httptest server's transport — so no request touches the
// network.
var ghClientTransport http.RoundTripper

// clientOptions builds the go-gh client options. When a test transport is
// installed we also supply a dummy token + host so go-gh skips its auth/host
// resolution entirely (fully hermetic: no gh config, no network). In
// production all three are left empty and go-gh resolves them exactly as the
// gh CLI would.
func clientOptions() ghapi.ClientOptions {
	if ghClientTransport != nil {
		return ghapi.ClientOptions{
			Transport: ghClientTransport,
			AuthToken: "test-token",
			Host:      "github.com",
		}
	}
	return ghapi.ClientOptions{}
}

// newRESTClient / newGraphQLClient construct the go-gh clients used for all
// in-process transport. They are vars so a test can replace the whole client
// with a mock (see the fakeGraphQL/fakeREST helpers) without a fake HTTP layer.
var newRESTClient = func() (restDoer, error) {
	c, err := ghapi.NewRESTClient(clientOptions())
	if err != nil {
		return nil, err
	}
	return c, nil
}

var newGraphQLClient = func() (graphQLDoer, error) {
	c, err := ghapi.NewGraphQLClient(clientOptions())
	if err != nil {
		return nil, err
	}
	return c, nil
}

// graphQLQuery is the single generic GraphQL helper (R6.1). It folds the
// response-envelope + error boilerplate that used to be copy-pasted across
// checks.go / review_threads.go / discussion.go / review_state.go: build the
// client, execute, and unmarshal the `data` object into T. go-gh handles the
// `{data, errors}` envelope and returns a *api.GraphQLError when the server
// reports GraphQL errors, so callers only deal with a typed data struct or a
// plain error.
func graphQLQuery[T any](ctx context.Context, query string, vars map[string]any) (T, error) {
	var out T
	c, err := newGraphQLClient()
	if err != nil {
		return out, fmt.Errorf("gh graphql client: %w", err)
	}
	start := time.Now()
	derr := c.DoWithContext(ctx, query, vars, &out)
	applog.GHInvocation([]string{"api", "graphql"}, time.Since(start), derr)
	if derr != nil {
		var zero T
		return zero, derr
	}
	return out, nil
}

// ghAPIGet issues a REST GET to apiPath and unmarshals the JSON response into
// out. Non-2xx responses come back as an *APIError carrying the real HTTP
// status code (via apiErrorFrom), so callers keep the same taxonomy the old
// gh-CLI shell-out produced.
func ghAPIGet(ctx context.Context, apiPath string, out interface{}) error {
	c, err := newRESTClient()
	if err != nil {
		return fmt.Errorf("gh rest client: %w", err)
	}
	start := time.Now()
	derr := c.DoWithContext(ctx, http.MethodGet, apiPath, nil, out)
	applog.GHInvocation([]string{"api", apiPath}, time.Since(start), derr)
	if derr != nil {
		return apiErrorFrom(derr, apiPath)
	}
	return nil
}

// ghAPIPost issues a REST POST to apiPath with a JSON body. It returns the raw
// go-gh error on failure so the caller can enrich the *APIError with the
// commit/comment context it knows about (see PostReview et al.). Success
// discards the response body — the post endpoints echo the created resource,
// which the callers do not use.
func ghAPIPost(ctx context.Context, apiPath string, body []byte) error {
	c, err := newRESTClient()
	if err != nil {
		return fmt.Errorf("gh rest client: %w", err)
	}
	start := time.Now()
	derr := c.DoWithContext(ctx, http.MethodPost, apiPath, bytes.NewReader(body), nil)
	applog.GHInvocation([]string{"api", apiPath, "--method", "POST"}, time.Since(start), derr)
	return derr
}

// apiErrorFrom converts a go-gh transport error into the package's *APIError
// taxonomy, feeding it the real HTTP status code, the per-field error items,
// and any Retry-After hint. When err is not an *api.HTTPError (e.g. a network
// failure) we still return a non-nil *APIError with status 0 and the raw
// message so callers keep a uniform error shape. Always returns non-nil when
// err is non-nil.
func apiErrorFrom(err error, apiPath string) *APIError {
	if err == nil {
		return nil
	}
	ae := &APIError{APIPath: apiPath, RawBody: err.Error(), Message: err.Error()}
	var he *ghapi.HTTPError
	if errors.As(err, &he) {
		ae.Status = he.StatusCode
		if strings.TrimSpace(he.Message) != "" {
			ae.Message = he.Message
		}
		for _, it := range he.Errors {
			ae.Errors = append(ae.Errors, APIErrorItem{
				Resource: it.Resource,
				Code:     it.Code,
				Field:    it.Field,
				Message:  it.Message,
			})
		}
		if ra := retryAfterFromHeaders(he.Headers); ra > 0 {
			ae.RetryAfter = ra
		}
	}
	ae.HumanReason = inferHumanReason(ae)
	return ae
}

// retryAfterFromHeaders reads GitHub's Retry-After header (delta-seconds or an
// HTTP-date) and returns it as a duration, or 0 when absent/unparseable.
func retryAfterFromHeaders(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
