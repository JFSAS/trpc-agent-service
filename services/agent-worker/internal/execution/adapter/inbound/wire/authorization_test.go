package wire

import (
	"encoding/json"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"os"
	"reflect"
	"testing"
)

func TestAuthorizationPreservedAndDigestBound(t *testing.T) {
	raw, e := os.ReadFile("../../../../../../../api/events/execution/v1/fixtures/valid/telegram-authorized.json")
	if e != nil {
		t.Fatal(e)
	}
	req, e := Decode(raw)
	if e != nil || req.Authorization == nil || req.Authorization.PrincipalID != "principal-1" || req.Authorization.PolicyRevision != 4 {
		t.Fatal(req, e)
	}
	saved, _ := json.Marshal(req)
	var again domain.Requested
	if e = json.Unmarshal(saved, &again); e != nil || !reflect.DeepEqual(again, req) {
		t.Fatal("lost persisted fact", e)
	}
	var body map[string]any
	json.Unmarshal(raw, &body)
	a := body["authorization"].(map[string]any)
	a["principal_revision"] = 3
	changed, _ := json.Marshal(body)
	other, e := Decode(changed)
	if e != nil || other.RunDigest == req.RunDigest || other.EventDigest == req.EventDigest {
		t.Fatal("authorization omitted from digest", e)
	}
	delete(body, "authorization")
	legacy, _ := json.Marshal(body)
	old, e := Decode(legacy)
	if e != nil || old.Authorization != nil || old.RunDigest == req.RunDigest {
		t.Fatal("legacy downgrade identity", e)
	}
}
