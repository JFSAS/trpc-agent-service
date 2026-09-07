package channelv1

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func snapshotFixture(t *testing.T, n int) (AuthorizationSnapshotManifest, []AuthorizationSnapshotPage) {
	t.Helper()
	id := AuthorizationSnapshotIdentity{1, "scope", "11111111-1111-4111-8111-111111111111", "tenant", "account", "wecom", 3}
	d := NewPrincipalSetDigest()
	rows := make([]AuthorizationPrincipal, 0, n)
	for i := 0; i < n; i++ {
		r := AuthorizationPrincipal{fmt.Sprintf("p_%04d", i), fmt.Sprintf("user_%d", i), "ACTIVE", 1}
		if err := d.Add(r); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	count, digest := d.Result()
	m := AuthorizationSnapshotManifest{id, 2, true, PolicyReference{"policy", 1, "sha256:" + strings.Repeat("a", 64)}, time.Now().UTC(), 30000, count, digest}
	pages := []AuthorizationSnapshotPage{}
	cursor := ""
	for start := 0; ; start += AuthorizationPageSize {
		end := min(start+AuthorizationPageSize, n)
		p := AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: id, AfterPrincipalID: cursor, Principals: append([]AuthorizationPrincipal{}, rows[start:end]...), Complete: end == n}
		if !p.Complete {
			p.NextPrincipalID = rows[end-1].PrincipalID
		}
		pages = append(pages, p)
		if p.Complete {
			break
		}
		cursor = p.NextPrincipalID
	}
	return m, pages
}
func TestAuthorizationSnapshotCompletenessProof(t *testing.T) {
	for _, n := range []int{0, 1, 128, 129, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			m, pages := snapshotFixture(t, n)
			p, err := NewAuthorizationSnapshotProof(m)
			if err != nil {
				t.Fatal(err)
			}
			if p.Finish() == nil {
				t.Fatal("unread snapshot accepted")
			}
			for _, page := range pages {
				raw, _ := json.Marshal(page)
				decoded, e := DecodeAuthorizationSnapshotPage(raw)
				if e != nil {
					t.Fatal(e)
				}
				if e = p.Add(decoded); e != nil {
					t.Fatal(e)
				}
			}
			if err = p.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"generation", "scope", "tenant", "epoch", "account", "provider", "omit", "reorder", "truncate", "tamper", "count", "root", "extra"} {
		t.Run(name, func(t *testing.T) {
			m, pages := snapshotFixture(t, 129)
			switch name {
			case "generation":
				pages[1].Generation++
			case "scope":
				pages[1].ScopeID = "other"
			case "tenant":
				pages[1].TenantID = "other"
			case "epoch":
				pages[1].SourceEpoch = "22222222-2222-4222-8222-222222222222"
			case "account":
				pages[1].AccountID = "other"
			case "provider":
				pages[1].Provider = "telegram"
			case "omit":
				pages = pages[1:]
			case "reorder":
				pages[0], pages[1] = pages[1], pages[0]
			case "truncate":
				pages = pages[:1]
			case "tamper":
				pages[1].Principals[0].State = "REVOKED"
			case "count":
				m.PrincipalCount++
			case "root":
				m.PrincipalDigest = "sha256:" + strings.Repeat("b", 64)
			case "extra":
				pages = append(pages, pages[1])
			}
			p, err := NewAuthorizationSnapshotProof(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range pages {
				_ = p.Add(page)
			}
			if p.Finish() == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}
func TestAuthorizationSnapshotClosedAndBounded(t *testing.T) {
	m, pages := snapshotFixture(t, 1)
	raw, _ := json.Marshal(m)
	for _, body := range [][]byte{append([]byte(`{"unknown":true,`), raw[1:]...), []byte(`{"schema_version":1,"schema_version":1}`), make([]byte, MaxAuthorizationManifestBytes+1)} {
		if out, e := DecodeAuthorizationSnapshotManifest(body); e == nil || out.ScopeID != "" {
			t.Fatal("invalid manifest", e)
		}
	}
	for _, value := range []string{"", "space id", "\n", "\u2003", strings.Repeat("界", 400)} {
		p := pages[0]
		p.Principals = append([]AuthorizationPrincipal{}, p.Principals...)
		p.Principals[0].ExternalUserID = value
		b, _ := json.Marshal(p)
		if out, e := DecodeAuthorizationSnapshotPage(b); e == nil || len(out.Principals) != 0 {
			t.Fatal("invalid external id", e)
		}
	}
	d := NewPrincipalSetDigest()
	r := pages[0].Principals[0]
	if d.Add(r) != nil || d.Add(r) == nil {
		t.Fatal("duplicate principal accepted")
	}
}

func TestAuthorizationSnapshotTelegramIdentityCanonical(t *testing.T) {
	_, pages := snapshotFixture(t, 1)
	for _, id := range []string{"00012", "0", "user", "+12", "-12"} {
		p := pages[0]
		p.Provider = "telegram"
		p.Principals = append([]AuthorizationPrincipal{}, p.Principals...)
		p.Principals[0].ExternalUserID = id
		raw, _ := json.Marshal(p)
		if _, err := DecodeAuthorizationSnapshotPage(raw); err == nil {
			t.Fatal("non-canonical Telegram identity")
		}
	}
	p := pages[0]
	p.Provider = "telegram"
	p.Principals[0].ExternalUserID = "12"
	raw, _ := json.Marshal(p)
	if _, err := DecodeAuthorizationSnapshotPage(raw); err != nil {
		t.Fatal(err)
	}
}
