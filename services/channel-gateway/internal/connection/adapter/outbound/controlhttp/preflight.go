package controlhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	p "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/preflight"
	c "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/domain/accountcatalog"
)

const preflightResponseLimit = 20 << 10

// PreflightClient has a dedicated mTLS pool: diagnostics cannot occupy the
// normal catalog/registration transport. No runtime-account bypass is involved.
type PreflightClient struct{ client *Client }

func NewPreflight(o Options) (*PreflightClient, error) {
	cl, err := New(o)
	if err != nil {
		return nil, p.ErrInvalid
	}
	return &PreflightClient{client: cl}, nil
}
func (cl *PreflightClient) Close() { cl.client.Close() }

// These DTOs are deliberately private. Control owns the wire contract; neither
// runtime ResolveRequest nor an application grant can be marshaled as that wire.
type preflightClaimRequest struct {
	SchemaVersion int     `json:"schema_version"`
	ScopeID       string  `json:"scope_id"`
	SourceEpoch   string  `json:"source_epoch"`
	InstanceEpoch string  `json:"instance_epoch"`
	RequestID     string  `json:"claim_request_id"`
	Token         string  `json:"claim_token"`
	ConfigDigest  string  `json:"gateway_config_digest"`
	PublicOrigin  *string `json:"expected_public_origin"`
	OriginStatus  string  `json:"origin_status"`
	Limit         int     `json:"limit"`
}
type preflightCredential struct {
	Purpose    string `json:"purpose"`
	ID         string `json:"credential_id"`
	Version    int64  `json:"credential_version"`
	Configured bool   `json:"configured"`
}
type preflightGrant struct {
	SchemaVersion           int                 `json:"schema_version"`
	ServerTime              time.Time           `json:"server_time"`
	ID                      string              `json:"preflight_id"`
	ScopeID                 string              `json:"scope_id"`
	SourceEpoch             string              `json:"source_epoch"`
	TenantID                string              `json:"tenant_id"`
	AccountID               string              `json:"account_id"`
	Provider                string              `json:"provider"`
	ProviderAccountID       string              `json:"provider_account_id"`
	AccountRevision         int64               `json:"account_revision"`
	ConnectionRevision      int64               `json:"connection_revision"`
	WebhookPath             string              `json:"webhook_path"`
	Credentials             preflightCredential `json:"credentials"`
	WebhookSecretConfigured bool                `json:"webhook_secret_configured"`
	LeaseEpoch              int64               `json:"lease_epoch"`
	LeaseExpiresAt          time.Time           `json:"lease_expires_at"`
	JobDeadlineAt           time.Time           `json:"job_deadline_at"`
	ConfigDigest            string              `json:"gateway_config_digest"`
}
type preflightLeaseRequest struct {
	SchemaVersion int    `json:"schema_version"`
	ScopeID       string `json:"scope_id"`
	SourceEpoch   string `json:"source_epoch"`
	InstanceEpoch string `json:"instance_epoch"`
	LeaseEpoch    int64  `json:"lease_epoch"`
	Token         string `json:"claim_token"`
}
type preflightResolved struct {
	SchemaVersion      int       `json:"schema_version"`
	ID                 string    `json:"preflight_id"`
	ConnectionRevision int64     `json:"connection_revision"`
	Purpose            string    `json:"purpose"`
	CredentialID       string    `json:"credential_id"`
	Version            int64     `json:"credential_version"`
	Value              string    `json:"value"`
	LeaseExpiresAt     time.Time `json:"lease_expires_at"`
}
type preflightCompleteRequest struct {
	SchemaVersion int       `json:"schema_version"`
	ScopeID       string    `json:"scope_id"`
	SourceEpoch   string    `json:"source_epoch"`
	InstanceEpoch string    `json:"instance_epoch"`
	LeaseEpoch    int64     `json:"lease_epoch"`
	Token         string    `json:"claim_token"`
	ConfigDigest  string    `json:"gateway_config_digest"`
	PublicOrigin  *string   `json:"expected_public_origin"`
	ObservedAt    time.Time `json:"observed_at"`
	Checks        []p.Check `json:"checks"`
}

func (cl *PreflightClient) validateClaim(r p.ClaimRequest) error {
	if cl == nil || cl.client == nil || p.ValidateConfig(r.Config) != nil || r.Config.ScopeID != cl.client.scope || r.Config.SourceEpoch != cl.client.epoch || !c.ValidEpoch(r.InstanceEpoch) || !c.ValidEpoch(r.RequestID) {
		return p.ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(r.Token.Reveal())
	if err != nil || len(raw) != 32 || len(r.Token.Reveal()) != 43 {
		return p.ErrInvalid
	}
	clear(raw)
	return nil
}

func (cl *PreflightClient) Claim(ctx context.Context, r p.ClaimRequest) (*p.Grant, error) {
	if err := cl.validateClaim(r); err != nil {
		return nil, err
	}
	req := preflightClaimRequest{1, r.Config.ScopeID, r.Config.SourceEpoch, r.InstanceEpoch, r.RequestID, r.Token.Reveal(), r.Config.Digest, r.Config.PublicOrigin, r.Config.OriginStatus, 1}
	raw, status, err := cl.exchange(ctx, "/internal/v1/channel-preflights:claim", req, 4<<10)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	if status == http.StatusNoContent {
		return nil, nil
	}
	var w preflightGrant
	if preflightDecode(raw, &w) != nil || w.SchemaVersion != 1 {
		return nil, p.ErrInvalid
	}
	g := p.Grant{PreflightID: w.ID, ScopeID: w.ScopeID, SourceEpoch: w.SourceEpoch, TenantID: w.TenantID, AccountID: w.AccountID, Provider: w.Provider, ProviderAccountID: w.ProviderAccountID, WebhookPath: w.WebhookPath, AccountRevision: w.AccountRevision, ConnectionRevision: w.ConnectionRevision, LeaseEpoch: w.LeaseEpoch, Credential: p.Credential{Purpose: w.Credentials.Purpose, ID: w.Credentials.ID, Version: w.Credentials.Version, Configured: w.Credentials.Configured}, WebhookSecretConfigured: w.WebhookSecretConfigured, ServerTime: w.ServerTime, LeaseExpiresAt: w.LeaseExpiresAt, JobDeadlineAt: w.JobDeadlineAt, ConfigDigest: w.ConfigDigest, Request: r}
	if err := p.ValidateGrant(g, r.Config); err != nil {
		return nil, err
	}
	return &g, nil
}

func (cl *PreflightClient) validateGrant(g p.Grant) error {
	if err := cl.validateClaim(g.Request); err != nil {
		return err
	}
	return p.ValidateGrant(g, g.Request.Config)
}
func leaseRequest(g p.Grant) preflightLeaseRequest {
	return preflightLeaseRequest{1, g.ScopeID, g.SourceEpoch, g.Request.InstanceEpoch, g.LeaseEpoch, g.Request.Token.Reveal()}
}
func (cl *PreflightClient) ResolveBotToken(ctx context.Context, g p.Grant) (p.Secret, error) {
	if err := cl.validateGrant(g); err != nil {
		return p.Secret{}, err
	}
	if !g.Credential.Configured {
		return p.Secret{}, p.ErrInvalid
	}
	raw, status, err := cl.exchange(ctx, "/internal/v1/channel-preflights/"+g.PreflightID+"/credentials:resolve", leaseRequest(g), 4<<10)
	if err != nil {
		return p.Secret{}, err
	}
	defer clear(raw)
	if status != http.StatusOK {
		return p.Secret{}, p.ErrInvalid
	}
	var w preflightResolved
	if preflightDecode(raw, &w) != nil || w.SchemaVersion != 1 || w.ID != g.PreflightID || w.ConnectionRevision != g.ConnectionRevision || w.Purpose != "telegram.bot_token" || w.Purpose != g.Credential.Purpose || w.CredentialID != g.Credential.ID || w.Version != g.Credential.Version || !w.LeaseExpiresAt.Equal(g.LeaseExpiresAt) || w.Value == "" || len(w.Value) > 16<<10 {
		return p.Secret{}, p.ErrInvalid
	}
	if ctx.Err() != nil {
		return p.Secret{}, p.ErrExpired
	}
	return p.NewSecret(w.Value), nil
}
func (cl *PreflightClient) Complete(ctx context.Context, g p.Grant, r p.Result) error {
	if err := cl.validateGrant(g); err != nil {
		return err
	}
	if err := validatePreflightResult(g, r); err != nil {
		return err
	}
	req := preflightCompleteRequest{1, g.ScopeID, g.SourceEpoch, g.Request.InstanceEpoch, g.LeaseEpoch, g.Request.Token.Reveal(), r.Config.Digest, r.Config.PublicOrigin, r.ObservedAt, r.Checks}
	raw, status, err := cl.exchange(ctx, "/internal/v1/channel-preflights/"+g.PreflightID+":complete", req, 16<<10)
	clear(raw)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return p.ErrInvalid
	}
	return nil
}

// exchange never returns an upstream body, URL, credential, or TLS detail as an
// error. It does not retry: the runner owns the original claim/result replay.
func (cl *PreflightClient) exchange(ctx context.Context, path string, payload any, requestLimit int) ([]byte, int, error) {
	if ctx == nil {
		return nil, 0, p.ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, 0, p.ErrExpired
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > requestLimit {
		return nil, 0, p.ErrInvalid
	}
	defer clear(body)
	// An HTTP-attempt deadline is not the diagnostic task/lease deadline.
	// Preserve the caller so an uncertain local timeout remains retryable.
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()
	u := *cl.client.base
	u.Path = path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, p.ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-store")
	res, err := cl.client.http.Do(req)
	if err != nil {
		if parentCtx.Err() != nil {
			return nil, 0, p.ErrExpired
		}
		return nil, 0, p.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		switch res.StatusCode {
		case 400, 422:
			return nil, 0, p.ErrInvalid
		case 401, 403, 404:
			return nil, 0, p.ErrDenied
		case 409:
			// Inspect only this closed code. The message/body is never surfaced.
			raw, _ := io.ReadAll(io.LimitReader(res.Body, 4097))
			defer clear(raw)
			var e struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if len(raw) <= 4096 && json.Unmarshal(raw, &e) == nil && e.Error.Code == "CHANNEL_PREFLIGHT_LEASE_EXPIRED" {
				return nil, 0, p.ErrExpired
			}
			return nil, 0, p.ErrConflict
		default:
			return nil, 0, p.ErrUnavailable
		}
	}
	if encoding := res.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, 0, p.ErrInvalid
	}
	if !headerNoStore(res.Header.Values("Cache-Control")) {
		return nil, 0, p.ErrInvalid
	}
	if res.StatusCode == http.StatusOK {
		media, _, e := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			return nil, 0, p.ErrInvalid
		}
	}
	if res.ContentLength > preflightResponseLimit {
		return nil, 0, p.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, preflightResponseLimit+1))
	if err != nil {
		clear(raw)
		if parentCtx.Err() != nil {
			return nil, 0, p.ErrExpired
		}
		return nil, 0, p.ErrUnavailable
	}
	if len(raw) > preflightResponseLimit || res.StatusCode == http.StatusNoContent && len(raw) != 0 {
		clear(raw)
		return nil, 0, p.ErrInvalid
	}
	if ctx.Err() != nil {
		clear(raw)
		if parentCtx.Err() != nil {
			return nil, 0, p.ErrExpired
		}
		return nil, 0, p.ErrUnavailable
	}
	return raw, res.StatusCode, nil
}
func headerNoStore(values []string) bool {
	for _, v := range values {
		for _, directive := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
				return true
			}
		}
	}
	return false
}

// preflightDecode is a temporary wire firewall until Control supplies the shared
// schemas. It rejects duplicate/case-folded keys, absent fields, null scalars,
// unsafe integers, deep JSON, invalid UTF-8, unknown fields and trailing values.
func preflightDecode(raw []byte, out any) error {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return p.ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := preflightJSONValue(dec, 0)
	if err != nil {
		return p.ErrInvalid
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return p.ErrInvalid
	}
	typ := reflect.TypeOf(out)
	if typ == nil || typ.Kind() != reflect.Pointer || preflightShape(value, typ.Elem()) != nil {
		return p.ErrInvalid
	}
	if json.Unmarshal(raw, out) != nil {
		return p.ErrInvalid
	}
	return nil
}
func preflightJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, p.ErrInvalid
	}
	tok, err := d.Token()
	if err != nil {
		return nil, p.ErrInvalid
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, p.ErrInvalid
			}
			s, ok := key.(string)
			if !ok {
				return nil, p.ErrInvalid
			}
			if _, exists := obj[s]; exists {
				return nil, p.ErrInvalid
			}
			v, err := preflightJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			obj[s] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, p.ErrInvalid
		}
		return obj, nil
	case '[':
		arr := []any{}
		for d.More() {
			v, err := preflightJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, p.ErrInvalid
		}
		return arr, nil
	default:
		return nil, p.ErrInvalid
	}
}
func preflightShape(value any, typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		if value == nil {
			return nil
		}
		return preflightShape(value, typ.Elem())
	}
	if typ == reflect.TypeOf(time.Time{}) {
		s, ok := value.(string)
		if !ok || !strings.HasSuffix(s, "Z") {
			return p.ErrInvalid
		}
		parsed, err := time.Parse(time.RFC3339Nano, s)
		if err != nil || parsed.IsZero() {
			return p.ErrInvalid
		}
		return nil
	}
	if value == nil {
		return p.ErrInvalid
	}
	switch typ.Kind() {
	case reflect.Struct:
		obj, ok := value.(map[string]any)
		if !ok || len(obj) != typ.NumField() {
			return p.ErrInvalid
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			key := field.Tag.Get("json")
			v, ok := obj[key]
			if !ok || preflightShape(v, field.Type) != nil {
				return p.ErrInvalid
			}
		}
	case reflect.String:
		if _, ok := value.(string); !ok {
			return p.ErrInvalid
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return p.ErrInvalid
		}
	case reflect.Int, reflect.Int64:
		n, ok := value.(json.Number)
		if !ok {
			return p.ErrInvalid
		}
		x, err := n.Int64()
		if err != nil || x > c.MaxRevision || x < -c.MaxRevision {
			return p.ErrInvalid
		}
	default:
		return p.ErrInvalid
	}
	return nil
}

var _ p.Control = (*PreflightClient)(nil)

func validatePreflightResult(g p.Grant, r p.Result) error {
	_, observedOffset := r.ObservedAt.Zone()
	if p.ValidateConfig(r.Config) != nil || r.Config.Digest != g.ConfigDigest || r.Config.ScopeID != g.ScopeID || r.Config.SourceEpoch != g.SourceEpoch || !sameOrigin(r.Config.PublicOrigin, g.Request.Config.PublicOrigin) || r.Config.OriginStatus != g.Request.Config.OriginStatus || r.ObservedAt.IsZero() || observedOffset != 0 || len(r.Checks) != 8 {
		return p.ErrInvalid
	}
	ids := []string{"credential_configuration", "bot_identity", "public_origin", "webhook_registration", "pending_updates", "delivery_errors", "recovery_materials", "delivery_verification"}
	for i, ch := range r.Checks {
		if ch.ID != ids[i] || len(ch.Details) > 4096 {
			return p.ErrInvalid
		}
	}
	checks := r.Checks
	var credentials struct {
		Bot     bool `json:"bot_token_configured"`
		Webhook bool `json:"webhook_secret_configured"`
	}
	if preflightDecode(checks[0].Details, &credentials) != nil || credentials.Bot != g.Credential.Configured || credentials.Webhook != g.WebhookSecretConfigured {
		return p.ErrInvalid
	}
	code, status := "CREDENTIALS_CONFIGURED", "PASS"
	if !credentials.Bot {
		code, status = "BOT_TOKEN_MISSING", "FAIL"
	} else if !credentials.Webhook {
		code, status = "WEBHOOK_SECRET_MISSING", "FAIL"
	}
	if !preflightCheckIs(checks[0], code, status) {
		return p.ErrInvalid
	}
	var identity struct {
		Match *bool `json:"identity_match"`
	}
	if preflightDecode(checks[1].Details, &identity) != nil {
		return p.ErrInvalid
	}
	switch checks[1].Code {
	case "BOT_IDENTITY_MATCH":
		if checks[1].Status != "PASS" || identity.Match == nil || !*identity.Match {
			return p.ErrInvalid
		}
	case "BOT_IDENTITY_MISMATCH":
		if checks[1].Status != "FAIL" || identity.Match == nil || *identity.Match {
			return p.ErrInvalid
		}
	case "TOKEN_REJECTED":
		if checks[1].Status != "FAIL" || identity.Match != nil {
			return p.ErrInvalid
		}
	case "NOT_EXECUTED":
		if checks[1].Status != "SKIPPED" || identity.Match != nil {
			return p.ErrInvalid
		}
	default:
		if !preflightProviderFailure(checks[1].Code) || checks[1].Status != "UNKNOWN" || identity.Match != nil {
			return p.ErrInvalid
		}
	}
	if !credentials.Bot && checks[1].Code != "NOT_EXECUTED" {
		return p.ErrInvalid
	}
	var origin struct {
		Validation string `json:"validation"`
	}
	status = "FAIL"
	if r.Config.OriginStatus == "PUBLIC_ORIGIN_STATIC_VALID" {
		status = "PASS"
	}
	if preflightDecode(checks[2].Details, &origin) != nil || origin.Validation != "STATIC_ONLY" || !preflightCheckIs(checks[2], r.Config.OriginStatus, status) {
		return p.ErrInvalid
	}
	var webhook struct {
		Presence *bool  `json:"presence"`
		Relation string `json:"relation"`
	}
	if preflightDecode(checks[3].Details, &webhook) != nil {
		return p.ErrInvalid
	}
	switch checks[3].Code {
	case "WEBHOOK_MATCH", "WEBHOOK_DIFFERENT":
		relation, status := "MATCH", "PASS"
		if checks[3].Code == "WEBHOOK_DIFFERENT" {
			relation, status = "DIFFERENT", "WARN"
		}
		if checks[3].Status != status || webhook.Presence == nil || !*webhook.Presence || webhook.Relation != relation || r.Config.PublicOrigin == nil {
			return p.ErrInvalid
		}
	case "WEBHOOK_NONE":
		if checks[3].Status != "WARN" || webhook.Presence == nil || *webhook.Presence || webhook.Relation != "NONE" {
			return p.ErrInvalid
		}
	case "WEBHOOK_COMPARISON_UNAVAILABLE":
		if checks[3].Status != "UNKNOWN" || webhook.Presence == nil || !*webhook.Presence || webhook.Relation != "UNKNOWN" || r.Config.PublicOrigin != nil {
			return p.ErrInvalid
		}
	case "TOKEN_REJECTED":
		if checks[3].Status != "FAIL" || webhook.Presence != nil || webhook.Relation != "UNKNOWN" {
			return p.ErrInvalid
		}
	case "NOT_EXECUTED":
		if checks[3].Status != "SKIPPED" || webhook.Presence != nil || webhook.Relation != "UNKNOWN" {
			return p.ErrInvalid
		}
	default:
		if !preflightProviderFailure(checks[3].Code) || checks[3].Status != "UNKNOWN" || webhook.Presence != nil || webhook.Relation != "UNKNOWN" {
			return p.ErrInvalid
		}
	}
	if checks[1].Code != "BOT_IDENTITY_MATCH" && checks[3].Code != "NOT_EXECUTED" {
		return p.ErrInvalid
	}
	var pending struct {
		Count *int64 `json:"pending_update_count"`
	}
	if preflightDecode(checks[4].Details, &pending) != nil {
		return p.ErrInvalid
	}
	if pending.Count == nil {
		if !preflightCheckIs(checks[4], "NOT_EXECUTED", "SKIPPED") {
			return p.ErrInvalid
		}
	} else {
		code, status := "PENDING_UPDATES_ZERO", "PASS"
		if *pending.Count < 0 {
			return p.ErrInvalid
		}
		if *pending.Count > 0 {
			code, status = "PENDING_UPDATES_PRESENT", "WARN"
		}
		if !preflightCheckIs(checks[4], code, status) {
			return p.ErrInvalid
		}
	}
	var delivery struct {
		HasError  *bool      `json:"has_last_error"`
		LastError *time.Time `json:"last_error_at"`
	}
	if preflightDecode(checks[5].Details, &delivery) != nil {
		return p.ErrInvalid
	}
	if delivery.HasError == nil {
		if delivery.LastError != nil || !preflightCheckIs(checks[5], "NOT_EXECUTED", "SKIPPED") {
			return p.ErrInvalid
		}
	} else if *delivery.HasError {
		if !preflightCheckIs(checks[5], "DELIVERY_ERROR_REPORTED", "WARN") {
			return p.ErrInvalid
		}
	} else if delivery.LastError != nil || !preflightCheckIs(checks[5], "DELIVERY_ERROR_NOT_REPORTED", "PASS") {
		return p.ErrInvalid
	}
	if webhook.Presence == nil {
		if pending.Count != nil || delivery.HasError != nil {
			return p.ErrInvalid
		}
	} else if pending.Count == nil || delivery.HasError == nil {
		return p.ErrInvalid
	}
	var recovery struct {
		Readable bool `json:"secret_token_readable"`
		Restore  bool `json:"restore_available"`
	}
	if preflightDecode(checks[6].Details, &recovery) != nil || recovery.Readable || recovery.Restore || !preflightCheckIs(checks[6], "RECOVERY_MATERIALS_UNAVAILABLE", "UNKNOWN") {
		return p.ErrInvalid
	}
	var verification struct {
		Verification string `json:"verification"`
	}
	if preflightDecode(checks[7].Details, &verification) != nil || verification.Verification != "NOT_TESTED" || !preflightCheckIs(checks[7], "DELIVERY_NOT_TESTED", "UNKNOWN") {
		return p.ErrInvalid
	}
	return nil
}
func sameOrigin(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func preflightCheckIs(c p.Check, code, status string) bool {
	return c.Code == code && c.Status == status
}
func preflightProviderFailure(code string) bool {
	switch code {
	case "PROVIDER_NETWORK", "PROVIDER_TIMEOUT", "PROVIDER_RATE_LIMITED", "PROVIDER_UNAVAILABLE", "PROVIDER_RESPONSE_INVALID":
		return true
	}
	return false
}
