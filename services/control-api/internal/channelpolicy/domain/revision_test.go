package domain_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	d "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

func TestPolicyDefinitionRevisionIntegrityAndCopy(t *testing.T) {
	definition := d.DefaultSessionDefinition()
	r, err := d.NewRevision("tenant-a", "session-a", "owner-a", d.Session, 1, definition, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	definition.Session.Partition = d.SharedConversation
	if r.Definition.Session.Partition != d.PerUserInConversation || r.Validate() != nil {
		t.Fatal("caller changed immutable candidate")
	}
	raw, _ := json.Marshal(r)
	decoded, err := d.Decode(raw)
	if err != nil || decoded.Digest != r.Digest {
		t.Fatal(err)
	}
	for _, field := range []string{"tenant", "id", "revision", "actor", "time", "body"} {
		changed, err := d.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		switch field {
		case "tenant":
			changed.TenantID = "other"
		case "id":
			changed.PolicyID = "other"
		case "revision":
			changed.Revision++
		case "actor":
			changed.PublishedBy = "other"
		case "time":
			changed.PublishedAt = changed.PublishedAt.Add(time.Second)
		case "body":
			changed.Definition.Session.Partition = d.SharedConversation
		}
		if changed.Validate() == nil {
			t.Fatal("substitution", field)
		}
	}
	for _, bad := range [][]byte{
		append(bytes.Clone(raw), []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"Schema_Version":1`), 1),
		bytes.Replace(raw, []byte(`"enabled":true`), []byte(`"enabled":true,"bypass":true`), 1),
		[]byte{0xff}, make([]byte, d.MaxDocumentBytes+1),
	} {
		if _, err := d.Decode(bad); err == nil {
			t.Fatal("invalid wire input accepted")
		}
	}
}
func TestPolicyDefinitionKindsAndQuotaBounds(t *testing.T) {
	valid := d.Definition{Enabled: true, Quota: &d.QuotaDefinition{PublicLimited: true, MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}}
	if _, err := d.NewRevision("tenant-a", "quota-a", "owner-a", d.Quota, d.MaxRevision, valid, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, definition := range []d.Definition{
		{}, d.DefaultSessionDefinition(),
		{Session: &d.SessionDefinition{Partition: d.PerUserInConversation}, Quota: valid.Quota},
		{Quota: &d.QuotaDefinition{PublicLimited: true, MaxConcurrentRuns: 0, MaxRunsPerMinute: 5}},
		{Quota: &d.QuotaDefinition{MaxConcurrentRuns: -1}},
		{Quota: &d.QuotaDefinition{MaxRunsPerMinute: d.MaxRevision + 1}},
	} {
		if _, err := d.NewRevision("tenant-a", "quota-a", "owner-a", d.Quota, 1, definition, time.Now()); err == nil {
			t.Fatal("invalid definition", definition)
		}
	}
	for _, revision := range []int64{0, -1, d.MaxRevision + 1} {
		if _, err := d.NewRevision("tenant-a", "quota-a", "owner-a", d.Quota, revision, valid, time.Now()); err == nil {
			t.Fatal(revision)
		}
	}
}
