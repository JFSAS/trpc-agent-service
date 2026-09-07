package domain

import (
	"strings"
	"testing"
)

func scope() Scope {
	return Scope{TenantID: "tenant", Provider: "telegram", AccountID: "account", ConversationID: "-100", ConversationKind: "group", ThreadID: "topic", BindingID: "binding", DeploymentRevisionID: "revision", PolicyID: "policy", PolicyRevision: 1, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Partition: PerUser, PrincipalID: "principal"}
}
func TestSessionScopeIsolationAndSwitchBack(t *testing.T) {
	s := scope()
	base, e := s.SessionID(1)
	if e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*Scope){"tenant": func(s *Scope) { s.TenantID = "other" }, "provider": func(s *Scope) { s.Provider = "wecom" }, "account": func(s *Scope) { s.AccountID = "other" }, "conversation": func(s *Scope) { s.ConversationID = "other" }, "topic": func(s *Scope) { s.ThreadID = "other" }, "binding": func(s *Scope) { s.BindingID = "other" }, "deployment": func(s *Scope) { s.DeploymentRevisionID = "other" }, "policy revision": func(s *Scope) { s.PolicyRevision++ }, "policy digest": func(s *Scope) { s.PolicyDigest = "sha256:" + strings.Repeat("b", 64) }, "principal": func(s *Scope) { s.PrincipalID = "other" }} {
		v := s
		change(&v)
		got, e := v.SessionID(1)
		if e != nil || got == base {
			t.Fatal(name, got, e)
		}
	}
	newer, _ := s.SessionID(2)
	if newer == base {
		t.Fatal("reset reused old session")
	}
	s.DeploymentRevisionID = "other"
	s.DeploymentRevisionID = "revision"
	back, _ := s.SessionID(1)
	if back != base {
		t.Fatal("switch-back lost scope")
	}
	shared := s
	shared.Partition = Shared
	shared.PrincipalID = ""
	sharedID, _ := shared.SessionID(1)
	namedShared := s
	namedShared.PrincipalID = "shared"
	perUser, _ := namedShared.SessionID(1)
	if sharedID == perUser {
		t.Fatal("partition tag collision")
	}
	for _, generation := range []int64{0, -1, MaxGeneration + 1} {
		if _, e = s.SessionID(generation); e == nil {
			t.Fatal("invalid generation")
		}
	}
	shared.PrincipalID = "principal"
	if shared.Validate() == nil {
		t.Fatal("shared identity carried implicit partition")
	}
}
func TestResetScopePermissionAndDigest(t *testing.T) {
	c := Reset{CommandID: "command", ActorID: "principal", Scope: scope(), ExpectedGeneration: 1}
	d, e := c.Digest()
	if e != nil || c.Operation() != "session.new" {
		t.Fatal(e)
	}
	c.ExpectedGeneration++
	other, _ := c.Digest()
	if d == other {
		t.Fatal("CAS omitted from identity")
	}
	c.ActorID = "other"
	if c.Validate() != ErrDenied {
		t.Fatal("reset others partition")
	}
	c.Scope.Partition = Shared
	c.Scope.PrincipalID = ""
	if c.Validate() != nil || c.Operation() != "session.reset_shared" {
		t.Fatal("shared permission mapping")
	}
}
