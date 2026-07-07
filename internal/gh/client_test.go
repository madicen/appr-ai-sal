package gh

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper so tests can serve
// canned responses without a real server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubGHResponder installs a fake HTTP transport that serves responses from
// respond(req) for the duration of the test, restoring the prior transport on
// cleanup. Because it swaps ghClientTransport (which go-gh's real clients use),
// every REST/GraphQL call in the test runs go-gh's actual parsing against the
// canned bytes with no network — keeping the suite hermetic while genuinely
// exercising the adopted transport layer (R6.2).
func stubGHResponder(t *testing.T, respond func(*http.Request) (int, string)) {
	t.Helper()
	prev := ghClientTransport
	ghClientTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := respond(r)
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})
	t.Cleanup(func() { ghClientTransport = prev })
}

// stubGraphQL installs a transport that returns the same GraphQL payload (a
// `{"data":...}` or `{"errors":...}` JSON string) for every request.
func stubGraphQL(t *testing.T, payload string) {
	t.Helper()
	stubGHResponder(t, func(*http.Request) (int, string) { return http.StatusOK, payload })
}

// readRequestBody drains and returns the request body as a string (used by
// pagination tests to inspect which cursor a GraphQL request carried).
func readRequestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.Body == nil {
		return ""
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(b)
}
