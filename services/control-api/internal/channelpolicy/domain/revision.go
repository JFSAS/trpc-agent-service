// Package domain owns published Channel Session/Quota definitions, not Worker
// session facts or quota consumption. Access policy publication references these
// immutable definitions; their existence is not a runtime access grant.
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
)

type Kind string

const (
	Session               Kind  = "session"
	Quota                 Kind  = "quota"
	PerUserInConversation       = "per_user_in_conversation"
	SharedConversation          = "shared_conversation"
	MaxRevision           int64 = 9007199254740991
	MaxDocumentBytes            = 64 * 1024
)

var ErrInvalid = errors.New("CHANNEL_POLICY_DEFINITION_INVALID")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ValidID(id string) bool            { return idPattern.MatchString(id) }
func ValidRevision(revision int64) bool { return revision > 0 && revision <= MaxRevision }

type SessionDefinition struct {
	Partition string `json:"partition"`
}
type QuotaDefinition struct {
	// MaxTotalModelTokens is a cumulative tenant token cap across revisions.
	// Nil is unconfigured, not unlimited; zero denies new model consumption.
	MaxTotalModelTokens *int64 `json:"max_total_model_tokens,omitempty"`
	PublicLimited       bool   `json:"public_limited"`
	MaxConcurrentRuns   int64  `json:"max_concurrent_runs"`
	MaxRunsPerMinute    int64  `json:"max_runs_per_minute"`
}
type Definition struct {
	Enabled bool               `json:"enabled"`
	Session *SessionDefinition `json:"session,omitempty"`
	Quota   *QuotaDefinition   `json:"quota,omitempty"`
}

func DefaultSessionDefinition() Definition {
	return Definition{Enabled: true, Session: &SessionDefinition{Partition: PerUserInConversation}}
}

type Revision struct {
	SchemaVersion int        `json:"schema_version"`
	TenantID      string     `json:"tenant_id"`
	PolicyID      string     `json:"policy_id"`
	Kind          Kind       `json:"kind"`
	Revision      int64      `json:"revision"`
	Definition    Definition `json:"definition"`
	PublishedBy   string     `json:"published_by"`
	PublishedAt   time.Time  `json:"published_at"`
	Digest        string     `json:"digest,omitempty"`
}

func (d Definition) valid(kind Kind) bool {
	switch kind {
	case Session:
		return d.Session != nil && d.Quota == nil && (d.Session.Partition == PerUserInConversation || d.Session.Partition == SharedConversation)
	case Quota:
		if d.Session != nil || d.Quota == nil {
			return false
		}
		q := d.Quota
		if q.MaxTotalModelTokens != nil && (*q.MaxTotalModelTokens < 0 || *q.MaxTotalModelTokens > MaxRevision) {
			return false
		}
		if q.MaxConcurrentRuns < 0 || q.MaxRunsPerMinute < 0 || q.MaxConcurrentRuns > MaxRevision || q.MaxRunsPerMinute > MaxRevision {
			return false
		}
		return !q.PublicLimited || (q.MaxConcurrentRuns > 0 && q.MaxRunsPerMinute > 0)
	default:
		return false
	}
}
func (d Definition) clone() Definition {
	if d.Session != nil {
		s := *d.Session
		d.Session = &s
	}
	if d.Quota != nil {
		q := *d.Quota
		if q.MaxTotalModelTokens != nil {
			value := *q.MaxTotalModelTokens
			q.MaxTotalModelTokens = &value
		}
		d.Quota = &q
	}
	return d
}

// NewRevision prepares owner content. OWNER authorization, revision CAS and
// atomic publication are the responsibility of the owner's command transaction.
func NewRevision(tenant, id, actor string, kind Kind, revision int64, definition Definition, now time.Time) (Revision, error) {
	r := Revision{SchemaVersion: 1, TenantID: tenant, PolicyID: id, Kind: kind, Revision: revision, Definition: definition.clone(), PublishedBy: actor, PublishedAt: now.UTC()}
	if !r.validFields() {
		return Revision{}, ErrInvalid
	}
	_, digest, err := canonical(r)
	if err != nil {
		return Revision{}, err
	}
	r.Digest = digest
	return r, nil
}
func (r Revision) validFields() bool {
	return r.SchemaVersion == 1 && ValidID(r.TenantID) && ValidID(r.PolicyID) && ValidID(r.PublishedBy) && ValidRevision(r.Revision) && !r.PublishedAt.IsZero() && r.Definition.valid(r.Kind)
}
func (r Revision) Validate() error {
	if !r.validFields() || !digestPattern.MatchString(r.Digest) {
		return ErrInvalid
	}
	expected := r.Digest
	r.Digest = ""
	_, actual, err := canonical(r)
	if err != nil || expected != actual {
		return ErrInvalid
	}
	return nil
}
func canonical(v any) ([]byte, string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, "", ErrInvalid
	}
	raw, err = jcs.Transform(raw)
	if err != nil || len(raw) > MaxDocumentBytes {
		return nil, "", ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return raw, "sha256:" + hex.EncodeToString(sum[:]), nil
}
func Decode(raw []byte) (Revision, error) {
	if len(raw) > MaxDocumentBytes || !utf8.Valid(raw) {
		return Revision{}, ErrInvalid
	}
	normalized, err := jcs.Transform(raw)
	if err != nil {
		return Revision{}, ErrInvalid
	}
	var r Revision
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || r.Validate() != nil {
		return Revision{}, ErrInvalid
	}
	encoded, _, err := canonical(r)
	if err != nil || !bytes.Equal(encoded, normalized) {
		return Revision{}, ErrInvalid
	}
	return r, nil
}
