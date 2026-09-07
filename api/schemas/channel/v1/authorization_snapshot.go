package channelv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
)

const AuthorizationPageSize = 128
const MaxAuthorizationPageBytes = 1 << 20
const MaxAuthorizationManifestBytes = 8192

// AuthorizationSnapshotIdentity binds a complete account-local read. Generation
// is a Control SQL fence, not a JetStream offset and not an authorization grant.
type AuthorizationSnapshotIdentity struct {
	SchemaVersion int    `json:"schema_version"`
	ScopeID       string `json:"scope_id"`
	SourceEpoch   string `json:"source_epoch"`
	TenantID      string `json:"tenant_id"`
	AccountID     string `json:"account_id"`
	Provider      string `json:"provider"`
	Generation    int64  `json:"generation"`
}
type AuthorizationSnapshotManifest struct {
	AuthorizationSnapshotIdentity
	AccountRevision       int64           `json:"account_revision"`
	AccountEnabled        bool            `json:"account_enabled"`
	Policy                PolicyReference `json:"policy"`
	CapturedAt            time.Time       `json:"captured_at"`
	AuthorizationMaxAgeMS int64           `json:"authorization_max_age_ms"`
	PrincipalCount        int64           `json:"principal_count"`
	PrincipalDigest       string          `json:"principal_digest"`
}
type AuthorizationPrincipal struct {
	PrincipalID    string `json:"principal_id"`
	ExternalUserID string `json:"external_user_id"`
	State          string `json:"state"`
	Revision       int64  `json:"revision"`
}
type AuthorizationSnapshotPage struct {
	AuthorizationSnapshotIdentity
	AfterPrincipalID string                   `json:"after_principal_id"`
	NextPrincipalID  string                   `json:"next_principal_id"`
	Principals       []AuthorizationPrincipal `json:"principals"`
	Complete         bool                     `json:"complete"`
}

// PrincipalSetDigest incrementally hashes ordered records, using O(1) memory.
// The protocol hashes the domain label followed by each JCS record and LF.
// JSON escapes embedded newlines, so record boundaries are unambiguous.
type PrincipalSetDigest struct {
	h     hash.Hash
	last  string
	count int64
}

func NewPrincipalSetDigest() *PrincipalSetDigest {
	h := sha256.New()
	_, _ = h.Write([]byte("channel.authorization.principals.v1\n"))
	return &PrincipalSetDigest{h: h}
}
func (d *PrincipalSetDigest) Add(p AuthorizationPrincipal) error {
	if d == nil || d.h == nil || p.PrincipalID <= d.last || d.count >= 9007199254740991 {
		return ErrInvalidDocument
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return ErrInvalidDocument
	}
	if Validate("authorization-principal.schema.json", raw) != nil || !validAuthorizationExternalID(p.ExternalUserID) {
		return ErrInvalidDocument
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return ErrInvalidDocument
	}
	_, _ = d.h.Write(canonical)
	_, _ = d.h.Write([]byte{'\n'})
	d.last = p.PrincipalID
	d.count++
	return nil
}
func (d *PrincipalSetDigest) Result() (int64, string) {
	return d.count, "sha256:" + hex.EncodeToString(d.h.Sum(nil))
}
func validAuthorizationExternalID(s string) bool {
	if !utf8.ValidString(s) || len(s) > 1024 || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func canonicalTelegramPrincipalID(s string) bool {
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func DecodeAuthorizationSnapshotManifest(raw []byte) (AuthorizationSnapshotManifest, error) {
	var m AuthorizationSnapshotManifest
	if len(raw) > MaxAuthorizationManifestBytes || Decode("authorization-snapshot-manifest.schema.json", raw, &m) != nil || m.CapturedAt.IsZero() {
		return AuthorizationSnapshotManifest{}, ErrInvalidDocument
	}
	return m, nil
}
func DecodeAuthorizationSnapshotPage(raw []byte) (AuthorizationSnapshotPage, error) {
	var p AuthorizationSnapshotPage
	if len(raw) > MaxAuthorizationPageBytes || Decode("authorization-snapshot-page.schema.json", raw, &p) != nil {
		return AuthorizationSnapshotPage{}, ErrInvalidDocument
	}
	last := p.AfterPrincipalID
	for _, v := range p.Principals {
		if v.PrincipalID <= last || !validAuthorizationExternalID(v.ExternalUserID) || p.Provider == "telegram" && !canonicalTelegramPrincipalID(v.ExternalUserID) {
			return AuthorizationSnapshotPage{}, ErrInvalidDocument
		}
		last = v.PrincipalID
	}
	if p.Complete {
		if p.NextPrincipalID != "" {
			return AuthorizationSnapshotPage{}, ErrInvalidDocument
		}
	} else if len(p.Principals) != AuthorizationPageSize || p.NextPrincipalID != last {
		return AuthorizationSnapshotPage{}, ErrInvalidDocument
	}
	return p, nil
}

// AuthorizationSnapshotProof verifies all sequential pages against a trusted
// manifest. Finish proves completeness only; callers must separately pin trusted
// scope/epoch/account, enforce freshness and resolve exact policy dependencies.
// A failed Add poisons the proof: no partial set can subsequently be installed.
type AuthorizationSnapshotProof struct {
	manifest     AuthorizationSnapshotManifest
	digest       *PrincipalSetDigest
	cursor       string
	done, failed bool
}

func NewAuthorizationSnapshotProof(m AuthorizationSnapshotManifest) (*AuthorizationSnapshotProof, error) {
	raw, _ := json.Marshal(m)
	if _, err := DecodeAuthorizationSnapshotManifest(raw); err != nil {
		return nil, err
	}
	return &AuthorizationSnapshotProof{manifest: m, digest: NewPrincipalSetDigest()}, nil
}
func (v *AuthorizationSnapshotProof) Add(p AuthorizationSnapshotPage) error {
	if v == nil {
		return ErrInvalidDocument
	}
	if v.failed || v.done {
		v.failed = true
		return ErrInvalidDocument
	}
	v.failed = true
	raw, _ := json.Marshal(p)
	if _, err := DecodeAuthorizationSnapshotPage(raw); err != nil {
		return err
	}
	if p.AuthorizationSnapshotIdentity != v.manifest.AuthorizationSnapshotIdentity || p.AfterPrincipalID != v.cursor {
		return ErrInvalidDocument
	}
	for _, r := range p.Principals {
		if err := v.digest.Add(r); err != nil {
			return err
		}
	}
	count, _ := v.digest.Result()
	if count > v.manifest.PrincipalCount {
		return ErrInvalidDocument
	}
	v.cursor = p.NextPrincipalID
	v.done = p.Complete
	v.failed = false
	return nil
}
func (v *AuthorizationSnapshotProof) Finish() error {
	if v == nil || v.failed || !v.done {
		return ErrInvalidDocument
	}
	n, d := v.digest.Result()
	if n != v.manifest.PrincipalCount || d != v.manifest.PrincipalDigest {
		return ErrInvalidDocument
	}
	return nil
}
