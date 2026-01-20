package metabase

import (
	"context"
	"io"
	"net/http"
	"strings"
)

// GetHTTPClient returns the underlying HTTP client from ClientWithResponses
func (c *ClientWithResponses) GetHTTPClient() (HttpRequestDoer, string, []RequestEditorFn) {
	if client, ok := c.ClientInterface.(*Client); ok {
		return client.Client, client.Server, client.RequestEditors
	}
	return nil, "", nil
}

// DoHTTPRequest is a helper to make custom HTTP requests using the same authentication
func (c *ClientWithResponses) DoHTTPRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	client, server, editors := c.GetHTTPClient()
	if client == nil {
		return nil, http.ErrNotSupported
	}

	if server == "" {
		// If server is empty, return an error with a clear message
		resp := &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(strings.NewReader("DoHTTPRequest: server URL is empty")),
		}
		return resp, nil
	}

	// Build URL - ensure exactly one slash between server and path
	url := strings.TrimSuffix(server, "/")
	if !strings.HasPrefix(path, "/") {
		url += "/"
	}
	url += path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for _, editor := range editors {
		if err := editor(ctx, req); err != nil {
			return nil, err
		}
	}

	return client.Do(req)
}
