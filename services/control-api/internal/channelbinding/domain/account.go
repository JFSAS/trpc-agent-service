package domain

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxVersion int64 = 1<<53 - 1
const MaxCredentialBytes = 16 * 1024
const MaxAccounts = 1000
const MaxSnapshotBytes = 2 * 1024 * 1024

type Provider string

const (
	Telegram Provider = "telegram"
	WeCom    Provider = "wecom"
)
const (
	TelegramBotToken      = "telegram.bot_token"
	TelegramWebhookSecret = "telegram.webhook_secret"
	WeComBotSecret        = "wecom.bot_secret"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var decimalPattern = regexp.MustCompile(`^[0-9]+$`)
var webhookSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidID(v string) bool     { return idPattern.MatchString(v) }
func ValidVersion(v int64) bool { return v > 0 && v <= MaxVersion }
func ValidEpoch(v string) bool  { return uuidPattern.MatchString(v) }
func NextVersion(v int64) (int64, error) {
	if !ValidVersion(v) || v == MaxVersion {
		return 0, failure(VersionExhausted, "")
	}
	return v + 1, nil
}
func RequiredPurposes(p Provider) []string {
	switch p {
	case Telegram:
		return []string{TelegramBotToken, TelegramWebhookSecret}
	case WeCom:
		return []string{WeComBotSecret}
	default:
		return nil
	}
}
func ValidPurpose(p Provider, purpose string) bool {
	return slices.Contains(RequiredPurposes(p), purpose)
}

// ConnectionConfig is closed by provider; callers cannot submit endpoints or paths.
type ConnectionConfig struct {
	WebhookPath string `json:"webhook_path,omitempty"`
	BotID       string `json:"bot_id,omitempty"`
}
type Account struct {
	TenantID           string           `json:"tenant_id"`
	ID                 string           `json:"account_id"`
	ScopeID            string           `json:"-"`
	Provider           Provider         `json:"provider"`
	ProviderAccountID  string           `json:"provider_account_id"`
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	Revision           int64            `json:"account_revision"`
	ConnectionRevision int64            `json:"connection_revision"`
	MinRouteGeneration int64            `json:"min_route_generation"`
	Enabled            bool             `json:"enabled"`
	Config             ConnectionConfig `json:"config"`
	CreatedBy          string           `json:"created_by"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

func NormalizeProviderAccountID(p Provider, v string) (string, error) {
	if len(v) == 0 || len(v) > 1024 || !utf8.ValidString(v) {
		return "", failure(InputInvalid, "/provider_account_id")
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", failure(InputInvalid, "/provider_account_id")
		}
	}
	if p == Telegram {
		if !decimalPattern.MatchString(v) {
			return "", failure(InputInvalid, "/provider_account_id")
		}
		v = strings.TrimLeft(v, "0")
		if v == "" {
			return "", failure(InputInvalid, "/provider_account_id")
		}
	}
	if p != Telegram && p != WeCom {
		return "", failure(InputInvalid, "/provider")
	}
	return v, nil
}
func normalizeMetadata(name, description string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 {
		return "", failure(InputInvalid, "/name")
	}
	if !utf8.ValidString(description) || utf8.RuneCountInString(description) > 4096 {
		return "", failure(InputInvalid, "/description")
	}
	return name, nil
}
func NewAccount(tenant, id, scope, actor string, provider Provider, physicalID, name, description string, now time.Time) (Account, error) {
	if !ValidID(tenant) || !ValidID(id) || !ValidID(scope) || !ValidID(actor) {
		return Account{}, failure(InputInvalid, "")
	}
	physicalID, err := NormalizeProviderAccountID(provider, physicalID)
	if err != nil {
		return Account{}, err
	}
	name, err = normalizeMetadata(name, description)
	if err != nil {
		return Account{}, err
	}
	a := Account{TenantID: tenant, ID: id, ScopeID: scope, Provider: provider, ProviderAccountID: physicalID, Name: name, Description: description, Revision: 1, ConnectionRevision: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if provider == Telegram {
		a.Config.WebhookPath = "/v1/telegram/" + id
	} else {
		a.Config.BotID = physicalID
	}
	return a, nil
}
func (a Account) Validate() error {
	physical, err := NormalizeProviderAccountID(a.Provider, a.ProviderAccountID)
	if err != nil || physical != a.ProviderAccountID || !ValidID(a.ID) || !ValidID(a.TenantID) || !ValidID(a.ScopeID) || !ValidVersion(a.Revision) || !ValidVersion(a.ConnectionRevision) || a.ConnectionRevision > a.Revision || a.MinRouteGeneration < 0 || a.MinRouteGeneration > MaxVersion {
		return failure(SourceIntegrity, "")
	}
	name, err := normalizeMetadata(a.Name, a.Description)
	if err != nil || name != a.Name {
		return failure(SourceIntegrity, "")
	}
	if a.Provider == Telegram {
		if a.Config.WebhookPath != "/v1/telegram/"+a.ID || a.Config.BotID != "" {
			return failure(SourceIntegrity, "/config")
		}
	} else if a.Config.BotID != physical || a.Config.WebhookPath != "" {
		return failure(SourceIntegrity, "/config")
	}
	return nil
}
func (a Account) ChangeMetadata(expected int64, name, description *string, now time.Time) (Account, bool, error) {
	if expected != a.Revision || !ValidVersion(expected) {
		return a, false, failure(RevisionConflict, "/expected_account_revision")
	}
	if name == nil && description == nil {
		return a, false, failure(InputInvalid, "")
	}
	n, d := a.Name, a.Description
	if name != nil {
		n = *name
	}
	if description != nil {
		d = *description
	}
	n, err := normalizeMetadata(n, d)
	if err != nil {
		return a, false, err
	}
	if n == a.Name && d == a.Description {
		return a, false, nil
	}
	next, err := NextVersion(a.Revision)
	if err != nil {
		return a, false, err
	}
	a.Name = n
	a.Description = d
	a.Revision = next
	a.UpdatedAt = now
	return a, true, nil
}
func (a Account) SetEnabled(expected int64, enabled bool, credentials []CredentialMeta, now time.Time) (Account, bool, error) {
	if expected != a.Revision || !ValidVersion(expected) {
		return a, false, failure(RevisionConflict, "/expected_account_revision")
	}
	if enabled {
		if err := ValidateCredentialSet(a.Provider, credentials, true); err != nil {
			return a, false, err
		}
	}
	if enabled == a.Enabled {
		return a, false, nil
	}
	next, err := a.AdvanceConnection(now)
	if err != nil {
		return a, false, err
	}
	next.Enabled = enabled
	return next, true, nil
}
func (a Account) AdvanceConnection(now time.Time) (Account, error) {
	r, err := NextVersion(a.Revision)
	if err != nil {
		return a, err
	}
	c, err := NextVersion(a.ConnectionRevision)
	if err != nil {
		return a, err
	}
	a.Revision = r
	a.ConnectionRevision = c
	a.UpdatedAt = now
	return a, nil
}
