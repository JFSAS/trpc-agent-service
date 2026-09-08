package domain

import (
	"strings"
	"testing"
)

func TestValidateReconciliation(t *testing.T) {
	for _, v := range []struct {
		after string
		limit int
		valid bool
	}{
		{"", 1, true}, {"run-123", 1000, true}, {"", 0, false}, {"", 1001, false}, {"bad cursor", 10, false}, {strings.Repeat("a", 129), 1, false},
	} {
		if err := ValidateReconciliation(v.after, v.limit); (err == nil) != v.valid {
			t.Fatalf("%+v: %v", v, err)
		}
	}
}
