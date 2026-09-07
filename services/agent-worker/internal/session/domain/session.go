// Package domain defines Worker-owned conversation scope and generation facts.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("SESSION_INVALID")
var ErrConflict = errors.New("SESSION_CONFLICT")
var ErrDenied = errors.New("SESSION_DENIED")

const MaxGeneration int64 = 9007199254740991
const PerUser = "per_user_in_conversation"
const Shared = "shared_conversation"

var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Scope struct {
	TenantID, Provider, AccountID, ConversationID, ThreadID, BindingID, DeploymentRevisionID string
	ConversationKind                                                                         string
	PolicyID                                                                                 string
	PolicyRevision                                                                           int64
	PolicyDigest                                                                             string
	Partition                                                                                string
	PrincipalID                                                                              string
}

func ValidActor(value string) bool { return id.MatchString(value) }
func external(s string, optional bool) bool {
	if optional && s == "" {
		return true
	}
	if s == "" || len(s) > 1024 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func (s Scope) Validate() error {
	for _, v := range []string{s.TenantID, s.AccountID, s.BindingID, s.DeploymentRevisionID, s.PolicyID} {
		if !id.MatchString(v) {
			return ErrInvalid
		}
	}
	if (s.ConversationKind != "private" && s.ConversationKind != "group") || (s.Provider != "telegram" && s.Provider != "wecom") || !external(s.ConversationID, false) || !external(s.ThreadID, true) || s.PolicyRevision < 1 || s.PolicyRevision > MaxGeneration || !digest.MatchString(s.PolicyDigest) {
		return ErrInvalid
	}
	if s.Partition == PerUser {
		if !id.MatchString(s.PrincipalID) {
			return ErrInvalid
		}
	} else if s.Partition != Shared || s.PrincipalID != "" {
		return ErrInvalid
	}
	return nil
}

// Canonical includes tagged partition identity and exact policy content. A policy
// update cannot silently merge histories, including switching away and back.
func (s Scope) Canonical() ([]byte, error) {
	if e := s.Validate(); e != nil {
		return nil, e
	}
	return json.Marshal([]string{"channel-session-v1", s.TenantID, s.Provider, s.AccountID, s.ConversationID, s.ConversationKind, s.ThreadID, s.BindingID, s.DeploymentRevisionID, s.PolicyID, strconv.FormatInt(s.PolicyRevision, 10), s.PolicyDigest, s.Partition, s.PrincipalID})
}
func stable(prefix string, raw []byte) string {
	sum := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(sum[:])
}
func (s Scope) Key() (string, error) {
	b, e := s.Canonical()
	if e != nil {
		return "", e
	}
	return stable("scope_", b), nil
}
func (s Scope) SessionID(generation int64) (string, error) {
	if generation < 1 || generation > MaxGeneration {
		return "", ErrInvalid
	}
	b, e := s.Canonical()
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal([]string{string(b), strconv.FormatInt(generation, 10)})
	if e != nil {
		return "", e
	}
	return stable("ses_", raw), nil
}

type Selection struct {
	ScopeKey, SessionID string
	Generation          int64
}
type Reset struct {
	CommandID, ActorID string
	Scope              Scope
	ExpectedGeneration int64
}

func (r Reset) Validate() error {
	if !id.MatchString(r.CommandID) || !id.MatchString(r.ActorID) || r.ExpectedGeneration < 1 || r.ExpectedGeneration >= MaxGeneration || r.Scope.Validate() != nil {
		return ErrInvalid
	}
	if r.Scope.Partition == PerUser && r.ActorID != r.Scope.PrincipalID {
		return ErrDenied
	}
	return nil
}
func (r Reset) Digest() (string, error) {
	if e := r.Validate(); e != nil {
		return "", e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return "", e
	}
	return stable("sha256:", b), nil
}
func (r Reset) Operation() string {
	if r.Scope.Partition == Shared {
		return "session.reset_shared"
	}
	return "session.new"
}
