package domain

import (
	"strings"
	"testing"
)

func TestRunQuotaFingerprintBindsIdentityAndPolicy(t *testing.T) {
	r := RunQuotaRequest{TenantID: "tenant", RunID: "run", InputDigest: "sha256:" + strings.Repeat("a", 64)}
	q := QuotaReference{ID: "quota", Revision: 1, Digest: "sha256:" + strings.Repeat("b", 64)}
	original, e := r.Fingerprint(q)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"tenant", "run", "input", "policy", "revision", "digest"} {
		a, b := r, q
		switch field {
		case "tenant":
			a.TenantID = "other"
		case "run":
			a.RunID = "other"
		case "input":
			a.InputDigest = q.Digest
		case "policy":
			b.ID = "other"
		case "revision":
			b.Revision++
		case "digest":
			b.Digest = r.InputDigest
		}
		got, e := a.Fingerprint(b)
		if e != nil || got == original {
			t.Fatal(field, got, e)
		}
	}
	r.RunID = ""
	if _, e = r.Fingerprint(q); e != ErrInvalid {
		t.Fatal(e)
	}
}
