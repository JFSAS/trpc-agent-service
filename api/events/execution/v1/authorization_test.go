package executionv1

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAuthorizationClosedShapeAndIdentity(t *testing.T) {
	raw, e := os.ReadFile("fixtures/valid/telegram-authorized.json")
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"tenant_id", "account_id", "provider", "binding_id", "route_generation", "external_user_id", "conversation_id", "decision", "operation", "fresh_until", "evaluated_at", "scope_id", "extra"} {
		t.Run(field, func(t *testing.T) {
			var body map[string]any
			json.Unmarshal(raw, &body)
			a := body["authorization"].(map[string]any)
			switch field {
			case "route_generation":
				a[field] = 2
			case "fresh_until":
				a[field] = "2026-09-08T00:00:31Z"
			case "evaluated_at":
				a[field] = "2026-09-08T00:00:30Z"
			case "scope_id":
				a[field] = nil
			default:
				a[field] = "wrong"
			}
			bad, _ := json.Marshal(body)
			if _, e := DecodeRunRequested(bad); e == nil {
				t.Fatal("invalid fact accepted")
			}
		})
	}
	for _, mode := range []string{"null", "missing_principal", "unknown_reason"} {
		t.Run(mode, func(t *testing.T) {
			var body map[string]any
			json.Unmarshal(raw, &body)
			a := body["authorization"].(map[string]any)
			switch mode {
			case "null":
				body["authorization"] = nil
			case "missing_principal":
				delete(a, "principal_id")
			case "unknown_reason":
				a["reason"] = "bypass"
			}
			bad, _ := json.Marshal(body)
			if _, e := DecodeRunRequested(bad); e == nil {
				t.Fatal("invalid fact accepted")
			}
		})
	}
}
