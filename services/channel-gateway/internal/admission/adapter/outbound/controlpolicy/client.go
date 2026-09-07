// Package controlpolicy reads immutable policy documents for projection building.
// It does not authorize admissions or establish current principal freshness.
package controlpolicy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

var (
	ErrInvalid      = errors.New("POLICY_READ_INVALID")
	ErrUnavailable  = errors.New("POLICY_READ_UNAVAILABLE")
	ErrUnauthorized = errors.New("POLICY_READ_UNAUTHORIZED")
	ErrIntegrity    = errors.New("POLICY_READ_INTEGRITY")
	ErrNotFound     = errors.New("POLICY_READ_NOT_FOUND")
)

type Options struct {
	BaseURL     string
	ScopeID     string
	SourceEpoch string
	RootCAs     *x509.CertPool
	Certificate tls.Certificate
}
type Client struct {
	endpoint     string
	scope, epoch string
	http         *http.Client
	transport    *http.Transport
}

var scopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var epochPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func New(o Options) (*Client, error) {
	u, err := url.Parse(o.BaseURL)
	if !scopePattern.MatchString(o.ScopeID) || !epochPattern.MatchString(o.SourceEpoch) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || o.RootCAs == nil || len(o.Certificate.Certificate) == 0 || o.Certificate.PrivateKey == nil {
		return nil, ErrInvalid
	}
	u.Path = "/internal/v1/channel-access-policies:resolve"
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: o.RootCAs.Clone(), Certificates: []tls.Certificate{o.Certificate}}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	return &Client{endpoint: u.String(), scope: o.ScopeID, epoch: o.SourceEpoch, transport: tr, http: &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.transport.CloseIdleConnections() }

// Fetch consumes an already trusted published notification and compares every
// scope/epoch/tenant/account/provider/reference field. Notification trust must be
// established by the broker adapter before calling Fetch. Success is historical
// content only; it must never reset an authorization freshness deadline.
func (c *Client) Fetch(ctx context.Context, event wire.AccessPolicyEvent) (wire.AccessPolicyDocument, error) {
	zero := wire.AccessPolicyDocument{}
	if ctx == nil {
		return zero, ErrInvalid
	}
	if event.ScopeID != c.scope || event.SourceEpoch != c.epoch {
		return zero, ErrIntegrity
	}
	if _, _, err := event.CanonicalJSON(); err != nil || event.EventType != wire.AccessPolicyPublishedEvent {
		return zero, ErrInvalid
	}
	body, err := json.Marshal(wire.AccessPolicyResolveRequest{SchemaVersion: 1, AccountID: event.AccountID, Reference: wire.PolicyReference{ID: event.PolicyID, Revision: event.PolicyRevision, Digest: event.PolicyDigest}})
	if err != nil || wire.Validate("access-policy-resolve-request.schema.json", body) != nil {
		return zero, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return zero, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, ErrUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return zero, ErrUnauthorized
	case http.StatusNotFound:
		return zero, ErrNotFound
	case http.StatusConflict:
		return zero, ErrIntegrity
	default:
		return zero, ErrUnavailable
	}
	media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || res.ContentLength > wire.MaxAccessPolicyResolveBytes {
		return zero, ErrIntegrity
	}
	if enc := res.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		return zero, ErrIntegrity
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, wire.MaxAccessPolicyResolveBytes+1))
	if err != nil {
		return zero, ErrUnavailable
	}
	out, err := wire.DecodeAccessPolicyResolveResponse(raw)
	if err != nil {
		return zero, ErrIntegrity
	}
	p := out.Policy
	if out.ScopeID != event.ScopeID || out.SourceEpoch != event.SourceEpoch || p.TenantID != event.TenantID || p.AccountID != event.AccountID || p.Provider != event.Provider || p.PolicyID != event.PolicyID || p.Revision != event.PolicyRevision || p.Digest != event.PolicyDigest {
		return zero, ErrIntegrity
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return p, nil
}
