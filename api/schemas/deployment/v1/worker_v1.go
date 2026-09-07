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
	model, ok := c.Resources.Models[node.ModelResource]
	if !ok || len(c.Resources.Models) != 1 || len(c.ResolvedRequirements.Models) != 1 {
		return reject("model closure")
	}
	for _, resourceKey := range c.ResolvedRequirements.Models {
		if resourceKey != node.ModelResource {
			return reject("model requirement binding")
		}
	}
	if node.Generation != nil && node.Generation.MaxOutputTokens != nil && *node.Generation.MaxOutputTokens > c.Execution.MaxOutputTokens {
		return reject("generation exceeds fixed single-output policy")
	}
	if model.Kind != "openai_compatible" || model.AdapterVersion != "openai-compatible-v1" || model.Credential.Purpose != "api_key" || !slices.Contains(model.Capabilities, "chat") {
		return reject("model adapter")
	}
	sessionKey, ok := c.StorageRoles["session"]
	session, exists := c.Resources.Storage[sessionKey]
	if !ok || !exists || len(c.StorageRoles) != 1 || len(c.Resources.Storage) != 1 {
		return reject("session-only storage is required; memory is deferred")
	}
	if session.Kind != "postgres_state" || session.AdapterVersion != "postgres-state-v1" || session.Credential.Purpose != "dsn" {
		return reject("session adapter")
	}
	if session.Destination.Username != WorkerV1SessionRuntimeRole {
		return ErrWorkerV1SessionRuntimeRole
	}
	endpoint, err := url.Parse(model.BaseURL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(model.BaseURL, "#") || endpoint.Hostname() == "" || !slices.Contains(c.Execution.AllowedEndpointHosts, strings.ToLower(endpoint.Hostname())) || !slices.Contains(c.Execution.AllowedEndpointHosts, strings.ToLower(session.Destination.Host)) {
		return reject("fixed endpoint closure")
	}
	expectedHosts := []string{strings.ToLower(endpoint.Hostname()), strings.ToLower(session.Destination.Host)}
	slices.Sort(expectedHosts)
	expectedHosts = slices.Compact(expectedHosts)
	actualHosts := append([]string(nil), c.Execution.AllowedEndpointHosts...)
	slices.Sort(actualHosts)
	if !slices.Equal(actualHosts, expectedHosts) {
		return reject("endpoint set must equal selected resource closure")
	}
	if model.Credential.AudienceDigest != CredentialAudienceDigest(model.Kind, model.BaseURL) || session.Credential.AudienceDigest != CredentialAudienceDigest(session.Kind, session.Destination) {
		return reject("credential audience does not match fixed destination")
	}
	if model.Credential.CredentialID == session.Credential.CredentialID {
		return reject("credential closure")
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
