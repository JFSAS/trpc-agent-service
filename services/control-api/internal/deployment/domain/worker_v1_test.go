package domain

import (
	deploymentv1 "github.com/liuzengh/trpc-agent-service/api/schemas/deployment/v1"
	agentdomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/agent/domain"
	"strings"
	"testing"
)

func TestWorkerV1ContractGatesAndPreservesLegacy(t *testing.T) {
	legacy := validCompileInput()
	if _, report := Compile(legacy); !report.Valid {
		t.Fatal(report)
	}
	if legacy.Platform.Version == deploymentv1.WorkerV1PlatformVersion {
		t.Fatal("legacy contract mutated")
	}
	current := legacy
	current.Platform = WorkerV1PlatformExecutionContract()
	if _, report := Compile(current); report.Valid {
		t.Fatal("legacy tools/composition accepted for worker-v1")
	}
	current.Agent.Spec.Nodes = map[string]agentdomain.Node{"assistant": {Kind: agentdomain.NodeKindLLM, Instruction: "Answer.", ModelSlot: "primary", ToolSlots: []string{}, KnowledgeSlots: []string{}}}
	current.Agent.Spec.Root = "assistant"
	delete(current.Profile.Spec.Storage, "memory")
	current.Agent.Spec.Requirements.Tools = nil
	current.Agent.Spec.Requirements.Knowledge = nil
	if compiled, report := Compile(current); report.Valid || len(compiled.CanonicalContent) != 0 || len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != DiagnosticStorageRoleUnsupported || report.Diagnostics[0].Path != "/resources/storage/session/destination/username" || !strings.Contains(report.Diagnostics[0].Message, "session runtime username must be session_runtime") {
		t.Fatalf("wrong session role should fail explicitly: %#v", report)
	}
	session := current.Profile.Spec.Storage["session"]
	session.Destination.Username = deploymentv1.WorkerV1SessionRuntimeRole
	current.Profile.Spec.Storage["session"] = session
	compiled, report := Compile(current)
	if !report.Valid {
		t.Fatalf("single llm failed: %#v", report)
	}
	if _, err := VerifyManifestContent(compiled.CanonicalContent, compiled.ContentDigest); err != nil {
		t.Fatalf("new contract rejected by immutable stored read: %v", err)
	}
	wire, err := deploymentv1.DecodeManifestContent(compiled.CanonicalContent)
	if err != nil {
		t.Fatal(err)
	}
	if err = deploymentv1.ValidateWorkerV1(wire, current.Platform.Digest); err != nil {
		t.Fatal(err)
	}
	if current.Platform.Digest == legacy.Platform.Digest {
		t.Fatal("execution matrix did not affect identity")
	}
}

func TestWorkerV1RoleRuleParticipatesInPinnedContractDigest(t *testing.T) {
	if got := WorkerV1PlatformExecutionContract().Digest; got != "sha256:9bcb3b8fec8a0778132ac08d7fdb4eae6247492a61d3b465493054a874df0d6b" {
		t.Fatalf("Worker role rule release identity = %s", got)
	}
	if got := DefaultPlatformExecutionContract().Digest; got != "sha256:e5c1019aa4fa0ea06b08f4966bc5cf154113b702f3ace93db6483fcc5a0d5d33" {
		t.Fatalf("historical platform-v1 identity changed = %s", got)
	}
}
