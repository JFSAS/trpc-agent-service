package natsadapter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/inbound/policynats"
)

func TestPolicyScopeDeclarationsAndConsumerContract(t *testing.T) {
	top, err := LoadTopology("../../../../../deploy/nats/streams.yaml")
	if err != nil {
		t.Fatal(err)
	}
	top.PolicyScopes = []string{"scope-a", "scope-b"}
	if err = top.Validate(); err != nil {
		t.Fatal(err)
	}
	if !top.HasPolicyScope("scope-a") || top.HasPolicyScope("scope-c") {
		t.Fatal("scope selection")
	}
	for _, scope := range top.PolicyScopes {
		if !reflect.DeepEqual(policyConsumerConfig(scope), policynats.Config(policynats.DurableName(scope))) {
			t.Fatal("runtime/reconciler contract drift")
		}
	}
	for _, scopes := range [][]string{{"*"}, {"a", "a"}, {""}, make([]string, 65)} {
		bad := top
		bad.PolicyScopes = scopes
		if bad.Validate() == nil {
			t.Fatal("invalid scopes accepted", scopes)
		}
	}
	bad := top
	bad.Streams = append([]StreamSpec{}, top.Streams...)
	for i := range bad.Streams {
		if bad.Streams[i].Name == wire.AccessPolicyStream {
			bad.Streams[i].MaxBytes = 1 << 20
		}
	}
	if bad.Validate() == nil {
		t.Fatal("incompatible runtime stream size accepted")
	}
}
func TestPolicyScopeACLExpansionIsExact(t *testing.T) {
	base, err := os.ReadFile("../../../../../deploy/nats/permissions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "permissions.yaml")
	raw := strings.Replace(string(base), "policy_scopes: []", "policy_scopes: [scope-a]", 1)
	if err = os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := RenderServerConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	name := wire.PolicyConsumerName("scope-a")
	for _, subject := range []string{"$JS.API.CONSUMER.INFO." + wire.AccessPolicyStream + "." + name, "$JS.API.CONSUMER.MSG.NEXT." + wire.AccessPolicyStream + "." + name, "$JS.ACK." + wire.AccessPolicyStream + "." + name + ".>"} {
		if !strings.Contains(string(got), subject) {
			t.Fatal("missing exact grant", subject)
		}
	}
	if strings.Contains(string(got), wire.PolicyConsumerName("scope-b")) || strings.Contains(string(got), "$JS.API.CONSUMER.MSG.NEXT."+wire.AccessPolicyStream+".*") {
		t.Fatal("cross-scope permission")
	}
	for _, replacement := range []string{"[scope-a, scope-a]", "['*']"} {
		os.WriteFile(file, []byte(strings.Replace(raw, "[scope-a]", replacement, 1)), 0600)
		if _, err = RenderServerConfig(file); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
