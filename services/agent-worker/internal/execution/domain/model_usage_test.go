package domain

import "testing"

func TestModelUsageIdentityAndKnownZero(t *testing.T) {
	g := Grant{AttemptID: "attempt", Run: Run{Request: Requested{RunID: "run", RunDigest: Digest([]byte("input")), Route: Route{TenantID: "tenant", ManifestDigest: Digest([]byte("manifest"))}}}}
	result := RuntimeResult{UsageKnown: true}
	digest, err := ModelUsageDigest(g, result)
	if err != nil {
		t.Fatal("known zero", err)
	}
	next := g
	next.AttemptID = "new-attempt"
	if ModelOperationID(next) == ModelOperationID(g) {
		t.Fatal("new call reuses operation")
	}
	changed := g
	changed.Parent = Head{Ref: "candidate", Digest: Digest([]byte("history"))}
	other, err := ModelUsageDigest(changed, result)
	if err != nil || other == digest {
		t.Fatal("history not bound", err)
	}
	result.UsageKnown = false
	if _, err = ModelUsageDigest(g, result); err == nil {
		t.Fatal("unknown zero accepted")
	}
	result = RuntimeResult{UsageKnown: true, InputTokens: 1, OutputTokens: 2, TotalTokens: 4}
	if _, err = ModelUsageDigest(g, result); err == nil {
		t.Fatal("inconsistent counts accepted")
	}
}
