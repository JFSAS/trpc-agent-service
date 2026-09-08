package deploymentv1

import (
	"encoding/json"
	datav1 "github.com/liuzengh/trpc-agent-service/api/runtime/data/v1"
)

func (r ManifestStorageResource) MarshalJSON() ([]byte, error) {
	if (r.Kind == "managed_artifact" && r.MetadataContract != ArtifactMetadataContract) || (r.Kind != "managed_artifact" && r.MetadataContract != "") {
		return nil, ErrInvalidManifest
	}
	if r.Kind == "managed_session" || r.Kind == "managed_memory" || r.Kind == "managed_artifact" {
		if r.Backend == nil || r.Credential != (CredentialUse{}) || r.Destination != (StorageDestination{}) {
			return nil, ErrInvalidManifest
		}
		return json.Marshal(struct {
			MetadataContract string           `json:"metadata_contract,omitempty"`
			Adapter          string           `json:"adapter_version"`
			Kind             string           `json:"kind"`
			Backend          *datav1.Snapshot `json:"backend"`
		}{r.MetadataContract, r.AdapterVersion, r.Kind, r.Backend})
	}
	if r.Backend != nil {
		return nil, ErrInvalidManifest
	}
	type plain ManifestStorageResource
	return json.Marshal(plain(r))
}
func (r ManifestKnowledgeResource) MarshalJSON() ([]byte, error) {
	if r.Kind == "managed_knowledge" {
		if r.Backend == nil || r.Credential != nil || r.Host != "" || r.Port != 0 || r.TLS || r.Collection != "" {
			return nil, ErrInvalidManifest
		}
		return json.Marshal(struct {
			Adapter    string                    `json:"adapter_version"`
			Kind       string                    `json:"kind"`
			Backend    *datav1.Snapshot          `json:"backend"`
			Embedding  ManifestEmbeddingResource `json:"embedding"`
			Capability string                    `json:"capability"`
		}{r.AdapterVersion, r.Kind, r.Backend, r.Embedding, r.Capability})
	}
	if r.Backend != nil {
		return nil, ErrInvalidManifest
	}
	type plain ManifestKnowledgeResource
	return json.Marshal(plain(r))
}

// The shared codec rejects cross-role descriptors before a runtime factory can
// consume them. Platform authorization and operational readiness are separate.
func validateManagedResourceRoles(c ManifestContent) error {
	for name, r := range c.Resources.Storage {
		role := ""
		switch r.Kind {
		case "managed_session":
			role = "session"
		case "managed_memory":
			role = "memory"
		case "managed_artifact":
			role = "artifact"
		default:
			continue
		}
		if name != role || r.Backend == nil || r.Backend.TenantID != c.TenantID || r.Backend.ValidateForRole(role) != nil {
			return ErrInvalidManifest
		}
	}
	for _, r := range c.Resources.Knowledge {
		if r.Kind != "managed_knowledge" {
			continue
		}
		if r.Backend == nil || r.Backend.TenantID != c.TenantID || r.Backend.ValidateForRole("knowledge") != nil || r.Backend.Qdrant.Dimensions != r.Embedding.Dimensions {
			return ErrInvalidManifest
		}
	}
	return nil
}
