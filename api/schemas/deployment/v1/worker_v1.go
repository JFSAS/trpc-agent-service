package deploymentv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// WorkerV1PlatformVersion is a new immutable execution contract identity. Legacy
// platform-v1 manifests remain readable but are not executable by Worker V1.
const WorkerV1PlatformVersion = "worker-v1"

// WorkerV1SessionRuntimeRole is the fixed append-only runtime principal from
// Database V1 provisioning. Draft profiles and historical contracts stay generic.
const WorkerV1SessionRuntimeRole = "session_runtime"

var ErrUnsupportedWorkerManifest = errors.New("unsupported Worker V1 manifest")

var ErrWorkerV1SessionRuntimeRole = fmt.Errorf("%w: session runtime username must be %s", ErrUnsupportedWorkerManifest, WorkerV1SessionRuntimeRole)

// ValidateWorkerV1 is the static publication/consumer gate, not a mutable
// capability service. expectedPlatformDigest is release-pinned; empty skips
// identity matching for the publisher while retaining every capability check.
func ValidateWorkerV1(c ManifestContent, expectedPlatformDigest string) error {
	reject := func(reason string) error { return fmt.Errorf("%w: %s", ErrUnsupportedWorkerManifest, reason) }
	if c.SchemaVersion != "v1" || c.CompilerVersion != "deployment-compiler-v1" || c.RuntimeContractVersion != "worker-manifest-v1" || c.PlatformContract.Version != WorkerV1PlatformVersion || (expectedPlatformDigest != "" && c.PlatformContract.Digest != expectedPlatformDigest) {
		return reject("contract identity")
	}
	// Summary and explicitly declared PostgreSQL/Redis Memory are executable.
	if c.Runtime != nil && (c.Runtime.Summary == nil || c.Runtime.Summary.Validate() != nil) {
		return reject("invalid session summary configuration")
	}
	for _, node := range c.AgentPlan.Nodes {
		if node.Artifact != nil {
			return reject("artifact is deferred")
		}
		if node.AddSessionSummary != nil && (!*node.AddSessionSummary || c.Runtime == nil) {
			return reject("summary consumption requires enabled runtime summary")
		}
	}
	if c.Execution.Backend != "worker-process-v1" || c.Execution.MaxRunSeconds <= 0 || c.Execution.MaxOutputTokens <= 0 {
		return reject("execution policy")
	}
	node, ok := c.AgentPlan.Nodes[c.AgentPlan.Root]
	if !ok || len(c.AgentPlan.Nodes) != 1 || node.Kind != "llm" || len(node.Children) > 0 || node.Body != "" || node.MaxIterations != 0 {
		return reject("exactly one llm root is required")
	}
	if len(node.ToolResources)+len(node.KnowledgeResources)+len(node.CallableEntries) > 0 || len(c.Resources.Tools)+len(c.Resources.Knowledge)+len(c.ResolvedRequirements.Tools)+len(c.ResolvedRequirements.Knowledge) > 0 {
		return reject("tools, knowledge and callable entries are deferred")
	}
	selectedModels := map[string]bool{node.ModelResource: true}
	if c.Runtime != nil {
		selectedModels[c.Runtime.Summary.ModelResource] = true
	}
	if len(c.Resources.Models) != len(selectedModels) || len(c.ResolvedRequirements.Models) != len(selectedModels) {
		return reject("model closure")
	}
	bindings := map[string]bool{}
	for _, key := range c.ResolvedRequirements.Models {
		if !selectedModels[key] || bindings[key] {
			return reject("model requirement binding")
		}
		bindings[key] = true
	}
	if node.Generation != nil && node.Generation.MaxOutputTokens != nil && *node.Generation.MaxOutputTokens > c.Execution.MaxOutputTokens {
		return reject("generation exceeds fixed single-output policy")
	}
	sessionKey, ok := c.StorageRoles["session"]
	session, exists := c.Resources.Storage[sessionKey]
	storageCount := 1
	if node.Memory != nil {
		storageCount++
	}
	if !ok || !exists || len(c.StorageRoles) != storageCount || len(c.Resources.Storage) != storageCount {
		return reject("storage must equal explicit Session and Memory closure")
	}
	expectedHosts := []string{}
	switch session.Kind {
	case "postgres_state":
		if session.AdapterVersion != "postgres-state-v1" || session.Credential.Purpose != "dsn" {
			return reject("session adapter")
		}
		if session.Destination.Username != WorkerV1SessionRuntimeRole {
			return ErrWorkerV1SessionRuntimeRole
		}
		if session.Credential.CredentialID == "" || session.Credential.AudienceDigest != CredentialAudienceDigest(session.Kind, session.Destination) {
			return reject("credential audience does not match fixed destination")
		}
		expectedHosts = append(expectedHosts, strings.ToLower(session.Destination.Host))
	case "managed_session":
		b := session.Backend
		if sessionKey != "session" || session.AdapterVersion != "managed-session-v1" || b == nil || b.ValidateForRole("session") != nil || b.TenantID != c.TenantID || b.Kind != "redis" {
			return reject("fixed managed Redis session backend")
		}
		if b.Redis.Username != WorkerV1SessionRuntimeRole {
			return ErrWorkerV1SessionRuntimeRole
		}
		d, err := b.Digest()
		if err != nil || session.Credential.CredentialID == "" || session.Credential.Purpose != "dsn_password" || session.Credential.AudienceDigest != d {
			return reject("managed session credential audience")
		}
		expectedHosts = append(expectedHosts, strings.ToLower(b.Redis.Host))
	default:
		return reject("session adapter")
	}
	credentials := map[string]CredentialUse{session.Credential.CredentialID: session.Credential}
	if node.Memory != nil {
		m := node.Memory
		if m.Validate() != nil || c.Execution.MaxToolCalls < 1 || c.Sources.Agent.AgentID == "" {
			return reject("invalid memory configuration")
		}
		key, ok := c.StorageRoles["memory"]
		resource, exists := c.Resources.Storage[key]
		if !ok || !exists || key != m.Resource || key == sessionKey || resource.Kind != "managed_memory" || resource.AdapterVersion != "managed-memory-v1" || resource.Backend == nil {
			return reject("memory adapter binding")
		}
		backend := resource.Backend
		d, err := backend.Digest()
		if err != nil || backend.ValidateForRole("memory") != nil || backend.TenantID != c.TenantID {
			return reject("fixed memory backend")
		}
		if (backend.Kind == "postgresql" && backend.PostgreSQL.Username != "memory_runtime") || (backend.Kind == "redis" && backend.Redis.Username != "memory_runtime") {
			return reject("fixed memory runtime identity")
		}
		u := resource.Credential
		if u.CredentialID == "" || u.Purpose != "dsn_password" || u.AudienceDigest != d {
			return reject("memory credential audience")
		}
		if prior, ok := credentials[u.CredentialID]; ok && prior != u {
			return reject("credential closure")
		}
		credentials[u.CredentialID] = u
		host, err := backend.EndpointHost()
		if err != nil {
			return reject("memory endpoint")
		}
		expectedHosts = append(expectedHosts, strings.ToLower(host))
	}
	for key := range selectedModels {
		model, exists := c.Resources.Models[key]
		if !exists || model.Kind != "openai_compatible" || model.AdapterVersion != "openai-compatible-v1" || model.Credential.CredentialID == "" || model.Credential.Purpose != "api_key" || !slices.Contains(model.Capabilities, "chat") {
			return reject("model adapter")
		}
		if key == node.ModelResource && node.Memory != nil && len(node.Memory.Tools) > 0 && !slices.Contains(model.Capabilities, "tool_call") {
			return reject("memory tools require model tool_call capability")
		}
		endpoint, err := url.Parse(model.BaseURL)
		if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(model.BaseURL, "#") || endpoint.Hostname() == "" {
			return reject("fixed endpoint closure")
		}
		if model.Credential.AudienceDigest != CredentialAudienceDigest(model.Kind, model.BaseURL) {
			return reject("credential audience does not match fixed destination")
		}
		if prior, ok := credentials[model.Credential.CredentialID]; ok && prior != model.Credential {
			return reject("credential closure")
		}
		credentials[model.Credential.CredentialID] = model.Credential
		expectedHosts = append(expectedHosts, strings.ToLower(endpoint.Hostname()))
	}
	slices.Sort(expectedHosts)
	expectedHosts = slices.Compact(expectedHosts)
	actualHosts := append([]string(nil), c.Execution.AllowedEndpointHosts...)
	slices.Sort(actualHosts)
	if !slices.Equal(actualHosts, expectedHosts) {
		return reject("endpoint set must equal selected resource closure")
	}
	return nil
}

// CredentialAudienceDigest preserves Profile V1's frozen JSON-array digest
// protocol (not JCS). Struct field order is intentional for StorageDestination.
func CredentialAudienceDigest(parts ...any) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
