package controlhttp

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
)

// post never exposes response bodies or URL/TLS diagnostics through errors.
// Current snapshot responses must prohibit caching; immutable historical reads
// retain their previous wire behavior. Endpoints are fixed by this adapter.
func (c *Client) post(ctx context.Context, endpoint string, body []byte, max int64, current bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if current {
		req.Header.Set("Cache-Control", "no-store")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusConflict:
		if current {
			return nil, ErrSnapshotChanged
		}
		return nil, ErrIntegrity
	default:
		return nil, ErrUnavailable
	}
	media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || res.ContentLength > max {
		return nil, ErrIntegrity
	}
	if enc := res.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return nil, ErrIntegrity
	}
	if current && !hasNoStore(res.Header.Values("Cache-Control")) {
		return nil, ErrIntegrity
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	if int64(len(raw)) > max {
		return nil, ErrIntegrity
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return raw, nil
}
func hasNoStore(values []string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "no-store") {
				return true
			}
		}
	}
	return false
}
