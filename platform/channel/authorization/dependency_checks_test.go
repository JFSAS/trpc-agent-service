package authorization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

func checkDocument(t *testing.T, d *wire.PolicyDefinitionDocument) []byte {
	t.Helper()
	d.Digest = ""
	raw, _ := json.Marshal(d)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(d)
	return raw
}
func TestDependencySemanticsNoImplicitGrant(t *testing.T) {
	for _, mode := range []string{"allow", "disabled_session", "disabled_quota", "missing", "corrupt", "foreign", "public_unbounded", "public_shared", "public_no_ceiling", "public_excess", "public_bounded", "ordinary_zero"} {
		t.Run(mode, func(t *testing.T) {
			s, q := dependency(t, "session"), dependency(t, "quota")
			s.Definition.Enabled = true
			q.Definition.Enabled = true
			policy := wire.AccessPolicyDocument{TenantID: "tenant", Body: wire.AccessPolicyBody{AccessMode: "ALLOWLIST"}}
			var ceiling *PublicQuotaCeiling
			var want error
			switch mode {
			case "disabled_session":
				s.Definition.Enabled = false
				want = ErrDependencyDisabled
			case "disabled_quota":
				q.Definition.Enabled = false
				want = ErrDependencyDisabled
			case "missing", "corrupt", "foreign":
				want = ErrDependencyIntegrity
			case "ordinary_zero":
				q.Definition.Quota.MaxConcurrentRuns = 0
				q.Definition.Quota.MaxRunsPerMinute = 0
			}
			if len(mode) >= 6 && mode[:6] == "public" {
				policy.Body.AccessMode = "PUBLIC_LIMITED"
				q.Definition.Quota.PublicLimited = true
				ceiling = &PublicQuotaCeiling{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
				switch mode {
				case "public_unbounded":
					q.Definition.Quota.PublicLimited = false
					q.Definition.Quota.MaxConcurrentRuns = 0
					want = ErrDependencyDenied
				case "public_shared":
					s.Definition.Session.Partition = "shared_conversation"
					want = ErrDependencyDenied
				case "public_no_ceiling":
					ceiling = nil
					want = ErrDependencyNotReady
				case "public_excess":
					q.Definition.Quota.MaxRunsPerMinute = 6
					want = ErrDependencyDenied
				}
			}
			sr, qr := checkDocument(t, &s), checkDocument(t, &q)
			policy.Body.SessionPolicy = wire.PolicyReference{ID: s.PolicyID, Revision: s.Revision, Digest: s.Digest}
			policy.Body.TenantQuota = wire.PolicyReference{ID: q.PolicyID, Revision: q.Revision, Digest: q.Digest}
			switch mode {
			case "missing":
				qr = nil
			case "corrupt":
				qr = []byte(`{}`)
			case "foreign":
				policy.TenantID = "other"
			}
			got, e := CheckPolicyDependencies(policy, sr, qr, ceiling)
			if !errors.Is(e, want) {
				t.Fatal(mode, e, want)
			}
			if want != nil && !reflect.DeepEqual(got, DependencyConstraints{}) {
				t.Fatal("error leaked usable constraints")
			}
			if mode == "ordinary_zero" && (got.MaxConcurrentRuns != 0 || got.MaxRunsPerMinute != 0) {
				t.Fatal("invented quota semantics")
			}
			if want == nil && got.Partition != "per_user_in_conversation" {
				t.Fatal(got)
			}
		})
	}
}
