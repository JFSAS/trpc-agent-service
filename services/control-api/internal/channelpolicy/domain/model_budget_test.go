package domain_test

import (
	"encoding/json"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	d "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	"testing"
	"time"
)

func TestModelBudgetOwnerToConsumerIntegrity(t *testing.T) {
	for _, cap := range []int64{0, 1000, d.MaxRevision} {
		input := cap
		definition := d.Definition{Enabled: true, Quota: &d.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5, MaxTotalModelTokens: &input}}
		owner, e := d.NewRevision("tenant", "quota", "owner", d.Quota, 1, definition, time.Now().UTC())
		if e != nil {
			t.Fatal(e)
		}
		input++
		if *owner.Definition.Quota.MaxTotalModelTokens != cap || owner.Validate() != nil {
			t.Fatal("caller changed published cap")
		}
		raw, _ := json.Marshal(owner)
		consumer, e := wire.DecodePolicyDefinitionDocument(raw)
		if e != nil || consumer.Definition.Quota.MaxTotalModelTokens == nil || *consumer.Definition.Quota.MaxTotalModelTokens != cap {
			t.Fatal("owner cap lost on consumer wire", e)
		}
		var body map[string]any
		json.Unmarshal(raw, &body)
		quota := body["definition"].(map[string]any)["quota"].(map[string]any)
		for _, value := range []any{nil, -1, 1.5, "1000", float64(d.MaxRevision) + 1} {
			quota["max_total_model_tokens"] = value
			changed, _ := json.Marshal(body)
			if _, e = wire.DecodePolicyDefinitionDocument(changed); e == nil {
				t.Fatal("invalid/changed cap accepted", value)
			}
		}
		delete(quota, "max_total_model_tokens")
		changed, _ := json.Marshal(body)
		if _, e = wire.DecodePolicyDefinitionDocument(changed); e == nil {
			t.Fatal("cap stripping bypassed digest")
		}
	}
	for _, cap := range []int64{-1, d.MaxRevision + 1} {
		_, e := d.NewRevision("tenant", "quota", "owner", d.Quota, 1, d.Definition{Quota: &d.QuotaDefinition{MaxTotalModelTokens: &cap}}, time.Now())
		if e == nil {
			t.Fatal("invalid cap published")
		}
	}
}
