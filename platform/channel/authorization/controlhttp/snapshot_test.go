package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

type authorizationFixture struct {
	manifest wire.AuthorizationSnapshotManifest
	policy   wire.AccessPolicyResolveResponse
	pages    []wire.AuthorizationSnapshotPage
}

func authorizationFixtureFor(t *testing.T, n int) authorizationFixture {
	t.Helper()
	policy, _ := policyFixture(t)
	id := wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: testEpoch, TenantID: policy.Policy.TenantID, AccountID: policy.Policy.AccountID, Provider: policy.Policy.Provider, Generation: 17}
	digest := wire.NewPrincipalSetDigest()
	records := make([]wire.AuthorizationPrincipal, 0, n)
	for i := 0; i < n; i++ {
		record := wire.AuthorizationPrincipal{PrincipalID: fmt.Sprintf("principal_%04d", i), ExternalUserID: fmt.Sprintf("user_%d", i), State: "ACTIVE", Revision: 1}
		if i%2 == 1 {
			record.State = "REVOKED"
		}
		if err := digest.Add(record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	count, root := digest.Result()
	m := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: id, AccountRevision: 3, AccountEnabled: true, Policy: wire.PolicyReference{ID: policy.Policy.PolicyID, Revision: policy.Policy.Revision, Digest: policy.Policy.Digest}, CapturedAt: time.Now().UTC(), AuthorizationMaxAgeMS: policy.Policy.Body.AuthorizationMaxAgeMS, PrincipalCount: count, PrincipalDigest: root}
	pages := []wire.AuthorizationSnapshotPage{}
	after := ""
	for begin := 0; ; begin += wire.AuthorizationPageSize {
		end := min(begin+wire.AuthorizationPageSize, n)
		page := wire.AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: id, AfterPrincipalID: after, Principals: append([]wire.AuthorizationPrincipal{}, records[begin:end]...), Complete: end == n}
		if !page.Complete {
			page.NextPrincipalID = records[end-1].PrincipalID
		}
		pages = append(pages, page)
		if page.Complete {
			break
		}
		after = page.NextPrincipalID
	}
	return authorizationFixture{m, policy, pages}
}
func (f authorizationFixture) target() AuthorizationTarget {
	return AuthorizationTarget{TenantID: f.manifest.TenantID, AccountID: f.manifest.AccountID, Provider: f.manifest.Provider}
}
func (f authorizationFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.TLS.Version < 0x0304 || r.Method != "POST" || r.URL.RawQuery != "" {
		t.Error("expected verified private mTLS POST")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	switch r.URL.Path {
	case "/internal/v1/channel-authorizations:snapshot":
		if wire.Validate("authorization-manifest-request.schema.json", raw) != nil {
			t.Error("invalid manifest request")
		}
		json.NewEncoder(w).Encode(f.manifest)
	case "/internal/v1/channel-access-policies:resolve":
		var q wire.AccessPolicyResolveRequest
		if wire.Decode("access-policy-resolve-request.schema.json", raw, &q) != nil || q.AccountID != f.manifest.AccountID || q.Reference != f.manifest.Policy {
			t.Error("policy reference did not come from manifest")
		}
		json.NewEncoder(w).Encode(f.policy)
	case "/internal/v1/channel-authorizations:page":
		var q struct {
			Generation  int64  `json:"generation"`
			SourceEpoch string `json:"source_epoch"`
			AccountID   string `json:"account_id"`
			After       string `json:"after_principal_id"`
		}
		if wire.Validate("authorization-page-request.schema.json", raw) != nil || json.Unmarshal(raw, &q) != nil || q.Generation != f.manifest.Generation || q.SourceEpoch != testEpoch || q.AccountID != f.manifest.AccountID {
			t.Error("invalid page request")
		}
		for _, page := range f.pages {
			if page.AfterPrincipalID == q.After {
				json.NewEncoder(w).Encode(page)
				return
			}
		}
		t.Error("unrecognized cursor")
		w.WriteHeader(409)
	default:
		t.Error("unexpected endpoint")
		w.WriteHeader(404)
	}
}
func TestReadAuthorizationCompleteBoundedStreamMTLS(t *testing.T) {
	for _, n := range []int{0, 1, 128, 129, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := authorizationFixtureFor(t, n)
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
			count, pages := 0, 0
			started := time.Now()
			out, err := cl.ReadAuthorization(context.Background(), f.target(), func(ctx context.Context, p wire.AuthorizationSnapshotPage) error {
				if _, ok := ctx.Deadline(); !ok || len(p.Principals) > 128 {
					t.Fatal("unbounded staging")
				}
				count += len(p.Principals)
				pages++
				return nil
			})
			if err != nil || count != n || pages != len(f.pages) || !reflect.DeepEqual(out.Policy, f.policy.Policy) || out.Manifest != f.manifest {
				t.Fatal("complete read", out, err)
			}
			if out.StartedAt.Before(started) || out.ExpiresAt.Sub(out.StartedAt) != 30*time.Second || !time.Now().Before(out.ExpiresAt) {
				t.Fatal("freshness was not anchored before request")
			}
		})
	}
}
func TestReadAuthorizationRejectsIncompleteOrMixedSnapshots(t *testing.T) {
	for _, name := range []string{"scope", "epoch", "tenant", "account", "provider", "root", "count", "policy_body", "policy_age", "page_generation", "page_tenant", "page_epoch", "page_identity", "page_state", "truncated", "reordered", "cursor", "revoked_tamper", "empty_incomplete", "unknown_manifest", "unknown_page", "duplicate_page"} {
		t.Run(name, func(t *testing.T) {
			f := authorizationFixtureFor(t, 129)
			target := f.target()
			switch name {
			case "scope":
				f.manifest.ScopeID = "other"
			case "epoch":
				f.manifest.SourceEpoch = "11111111-1111-4111-8111-111111111111"
			case "tenant":
				f.manifest.TenantID = "other"
			case "account":
				f.manifest.AccountID = "other"
			case "provider":
				f.manifest.Provider = "telegram"
			case "root":
				f.manifest.PrincipalDigest = "sha256:" + strings.Repeat("a", 64)
			case "count":
				f.manifest.PrincipalCount++
			case "policy_body":
				f.policy.Policy.Body.AllowedPrincipalIDs = []string{"tampered"}
			case "policy_age":
				f.manifest.AuthorizationMaxAgeMS = 29000
			case "page_generation":
				f.pages[1].Generation++
			case "page_tenant":
				f.pages[1].TenantID = "other"
			case "page_epoch":
				f.pages[1].SourceEpoch = "11111111-1111-4111-8111-111111111111"
			case "page_identity":
				f.pages[1].Principals[0].ExternalUserID = "wrong"
			case "page_state":
				f.pages[1].Principals[0].State = "INVALID"
			case "truncated":
				f.pages[0].Complete = true
				f.pages[0].NextPrincipalID = ""
			case "reordered":
				f.pages[0].Principals[0], f.pages[0].Principals[1] = f.pages[0].Principals[1], f.pages[0].Principals[0]
			case "cursor":
				f.pages[0].NextPrincipalID = "wrong"
			case "revoked_tamper":
				f.pages[0].Principals[1].State = "ACTIVE"
			case "empty_incomplete":
				f.pages[0].Principals = []wire.AuthorizationPrincipal{}
			}
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "unknown_manifest" && strings.HasSuffix(r.URL.Path, ":snapshot") {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					b, _ := json.Marshal(f.manifest)
					w.Write(append([]byte(`{"role":"OWNER",`), b[1:]...))
					return
				}
				if (name == "unknown_page" || name == "duplicate_page") && strings.HasSuffix(r.URL.Path, ":page") {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					b, _ := json.Marshal(f.pages[0])
					prefix := `{"extra":1,`
					if name == "duplicate_page" {
						prefix = `{"schema_version":1,`
					}
					w.Write(append([]byte(prefix), b[1:]...))
					return
				}
				f.serve(t, w, r)
			}))
			staged := 0
			out, err := cl.ReadAuthorization(context.Background(), target, func(context.Context, wire.AuthorizationSnapshotPage) error { staged++; return nil })
			if !errors.Is(err, ErrIntegrity) || !reflect.DeepEqual(out, AuthorizationRead{}) {
				t.Fatal("invalid snapshot yielded usable result", out, err)
			}
			if (name == "scope" || name == "epoch" || name == "tenant" || name == "account" || name == "provider" || name == "policy_body" || name == "policy_age") && staged != 0 {
				t.Fatal("staged before identity and policy validated")
			}
		})
	}
}
func TestReadAuthorizationStatusAndTransportGuards(t *testing.T) {
	for _, name := range []string{"changed", "forbidden", "missing", "unavailable", "redirect", "no_store", "compressed", "media", "oversized"} {
		t.Run(name, func(t *testing.T) {
			f := authorizationFixtureFor(t, 129)
			var manifests, pages atomic.Int64
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ":snapshot") {
					manifests.Add(1)
				}
				if strings.HasSuffix(r.URL.Path, ":page") && pages.Add(1) == 2 {
					status := 200
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					switch name {
					case "changed":
						status = 409
					case "forbidden":
						status = 403
					case "missing":
						status = 404
					case "unavailable":
						status = 503
					case "redirect":
						status = 302
						w.Header().Set("Location", "https://example.invalid/private")
					case "no_store":
						w.Header().Del("Cache-Control")
					case "compressed":
						w.Header().Set("Content-Encoding", "gzip")
					case "media":
						w.Header().Set("Content-Type", "text/plain")
					case "oversized":
						w.Header().Set("Content-Length", fmt.Sprint(wire.MaxAuthorizationPageBytes+1))
					}
					w.WriteHeader(status)
					if name == "oversized" {
						return
					}
					if status != 200 {
						io.WriteString(w, "sensitive-upstream-message")
						return
					}
					json.NewEncoder(w).Encode(f.pages[1])
					return
				}
				f.serve(t, w, r)
			}))
			staged := 0
			out, err := cl.ReadAuthorization(context.Background(), f.target(), func(context.Context, wire.AuthorizationSnapshotPage) error { staged++; return nil })
			want := ErrIntegrity
			switch name {
			case "changed":
				want = ErrSnapshotChanged
			case "forbidden":
				want = ErrUnauthorized
			case "missing":
				want = ErrNotFound
			case "redirect", "unavailable":
				want = ErrUnavailable
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(out, AuthorizationRead{}) || staged != 1 || manifests.Load() != 1 {
				t.Fatal("failed partial read must be discarded, not retried", out, err, staged, manifests.Load())
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("error leaked body")
			}
		})
	}
}
func TestReadAuthorizationDeadlineAndStagingFailure(t *testing.T) {
	for _, name := range []string{"slow_manifest", "slow_page", "slow_stage", "stage_error", "cancel", "caller_timeout"} {
		t.Run(name, func(t *testing.T) {
			f := authorizationFixtureFor(t, 1)
			f.manifest.AuthorizationMaxAgeMS = 80
			f.policy.Policy.Body.AuthorizationMaxAgeMS = 80
			sign(t, &f.policy.Policy)
			f.manifest.Policy.Digest = f.policy.Policy.Digest
			// A future server wall timestamp must never extend the local elapsed budget.
			f.manifest.CapturedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "slow_manifest" && strings.HasSuffix(r.URL.Path, ":snapshot") || name == "slow_page" && strings.HasSuffix(r.URL.Path, ":page") || name == "caller_timeout" {
					select {
					case <-time.After(150 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				f.serve(t, w, r)
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "caller_timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
			}
			out, err := cl.ReadAuthorization(ctx, f.target(), func(context.Context, wire.AuthorizationSnapshotPage) error {
				switch name {
				case "slow_stage":
					time.Sleep(150 * time.Millisecond)
				case "stage_error":
					return errors.New("database password=never-expose")
				case "cancel":
					cancel()
				}
				return nil
			})
			want := ErrSnapshotExpired
			switch name {
			case "stage_error":
				want = ErrSnapshotStage
			case "cancel":
				want = context.Canceled
			case "caller_timeout":
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(out, AuthorizationRead{}) {
				t.Fatal(name, out, err)
			}
		})
	}
}
func TestReadAuthorizationInvalidInputNeverSends(t *testing.T) {
	f := authorizationFixtureFor(t, 0)
	var calls atomic.Int64
	cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); f.serve(t, w, r) }))
	stage := func(context.Context, wire.AuthorizationSnapshotPage) error { return nil }
	for _, target := range []AuthorizationTarget{{}, {TenantID: "t", AccountID: "*", Provider: "wecom"}, {TenantID: "t", AccountID: "a", Provider: "unknown"}} {
		if _, err := cl.ReadAuthorization(context.Background(), target, stage); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := cl.ReadAuthorization(nil, f.target(), stage); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := cl.ReadAuthorization(context.Background(), f.target(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached server")
	}
}
