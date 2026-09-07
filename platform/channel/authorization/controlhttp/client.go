// Package controlpolicy reads immutable policy documents for projection building.
// It does not authorize admissions or establish current principal freshness.
package controlhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
	origin       string
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
	u.Path = ""
	origin := u.String()
	u.Path = "/internal/v1/channel-access-policies:resolve"
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: o.RootCAs.Clone(), Certificates: []tls.Certificate{o.Certificate}}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}
	return &Client{endpoint: u.String(), origin: origin, scope: o.ScopeID, epoch: o.SourceEpoch, transport: tr, http: &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
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
	return c.fetchReference(ctx, event.TenantID, event.AccountID, event.Provider, wire.PolicyReference{ID: event.PolicyID, Revision: event.PolicyRevision, Digest: event.PolicyDigest})
}

// fetchReference is shared by notification reads and current manifests. A
// snapshot is not forged into a publication event merely to reuse the resolver.
func (c *Client) fetchReference(ctx context.Context, tenant, account, provider string, ref wire.PolicyReference) (wire.AccessPolicyDocument, error) {
	zero := wire.AccessPolicyDocument{}
	body, err := json.Marshal(wire.AccessPolicyResolveRequest{SchemaVersion: 1, AccountID: account, Reference: ref})
	if err != nil || wire.Validate("access-policy-resolve-request.schema.json", body) != nil {
		return zero, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := c.post(ctx, c.endpoint, body, wire.MaxAccessPolicyResolveBytes, false)
	if err != nil {
		return zero, err
	}
	out, err := wire.DecodeAccessPolicyResolveResponse(raw)
	if err != nil {
		return zero, ErrIntegrity
	}
	p := out.Policy
	if out.ScopeID != c.scope || out.SourceEpoch != c.epoch || p.TenantID != tenant || p.AccountID != account || p.Provider != provider || p.PolicyID != ref.ID || p.Revision != ref.Revision || p.Digest != ref.Digest {
		return zero, ErrIntegrity
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return p, nil
}
