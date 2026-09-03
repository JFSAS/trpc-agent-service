package domain

import "encoding/json"

const (
	SchemaVersionV1 = "v1"

	MaxDocumentBytes      = 512 * 1024
	MaxModelResources     = 16
	MaxToolResources      = 64
	MaxKnowledgeResources = 32
	MaxStorageResources   = 16
	MaxCapabilities       = 2
)

type ModelKind string
type ToolKind string
type KnowledgeKind string
type StorageKind string
type AuthKind string

const (
	ModelKindOpenAICompatible ModelKind     = "openai_compatible"
	ToolKindMCPStreamableHTTP ToolKind      = "mcp_streamable_http"
	KnowledgeKindQdrantOpenAI KnowledgeKind = "qdrant_openai"
	StorageKindPostgresState  StorageKind   = "postgres_state"

	AuthKindNone   AuthKind = "none"
	AuthKindBearer AuthKind = "bearer"

	CapabilityChat            = "chat"
	CapabilityToolCall        = "tool_call"
	CapabilityWebSearch       = "web.search"
	CapabilityKnowledgeSearch = "knowledge.search"
	CapabilityStorageSession  = "storage.session"
	CapabilityStorageMemory   = "storage.memory"
)

// Spec is the typed, validated representation of RuntimeProfileSpec V1.
type Spec struct {
	SchemaVersion string                       `json:"schema_version"`
	Models        map[string]ModelResource     `json:"models"`
	Tools         map[string]ToolResource      `json:"tools"`
	Knowledge     map[string]KnowledgeResource `json:"knowledge"`
	Storage       map[string]StorageResource   `json:"storage"`
}

type ModelResource struct {
	Kind         ModelKind `json:"kind"`
	Model        string    `json:"model"`
	BaseURL      string    `json:"base_url"`
	APIKeyRef    string    `json:"api_key_ref"`
	Capabilities []string  `json:"capabilities"`
}

func (r ModelResource) ProvidedCapabilities() []string {
	return append([]string(nil), r.Capabilities...)
}

type ToolResource struct {
	Kind        ToolKind `json:"kind"`
	ServerURL   string   `json:"server_url"`
	ToolsetName string   `json:"toolset_name"`
	ToolName    string   `json:"tool_name"`
	Auth        ToolAuth `json:"auth"`
	Capability  string   `json:"capability"`
}

func (r ToolResource) ProvidedCapabilities() []string {
	return []string{r.Capability}
}

// ToolAuth is a closed discriminated union. The bearer branch contains one
// SecretRef; the none branch contains no inactive credential field.
type ToolAuth struct {
	Kind      AuthKind
	SecretRef string
}

func (a *ToolAuth) UnmarshalJSON(data []byte) error {
	var wire struct {
		Kind      AuthKind `json:"kind"`
		SecretRef string   `json:"secret_ref"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*a = ToolAuth{Kind: wire.Kind, SecretRef: wire.SecretRef}
	return nil
}

func (a ToolAuth) MarshalJSON() ([]byte, error) {
	switch a.Kind {
	case AuthKindBearer:
		return json.Marshal(struct {
			Kind      AuthKind `json:"kind"`
			SecretRef string   `json:"secret_ref"`
		}{a.Kind, a.SecretRef})
	default:
		return json.Marshal(struct {
			Kind AuthKind `json:"kind"`
		}{a.Kind})
	}
}

type KnowledgeResource struct {
	Kind            KnowledgeKind     `json:"kind"`
	Host            string            `json:"host"`
	Port            int64             `json:"port"`
	TLS             bool              `json:"tls"`
	Collection      string            `json:"collection"`
	QdrantAPIKeyRef string            `json:"qdrant_api_key_ref,omitempty"`
	Embedding       EmbeddingResource `json:"embedding"`
}

func (KnowledgeResource) ProvidedCapabilities() []string {
	return []string{CapabilityKnowledgeSearch}
}

type EmbeddingResource struct {
	Model      string `json:"model"`
	BaseURL    string `json:"base_url"`
	APIKeyRef  string `json:"api_key_ref"`
	Dimensions int64  `json:"dimensions"`
}

type StorageResource struct {
	Kind   StorageKind `json:"kind"`
	DSNRef string      `json:"dsn_ref"`
}

func (StorageResource) ProvidedCapabilities() []string {
	return []string{CapabilityStorageSession, CapabilityStorageMemory}
}

// CanonicalSpec is the immutable publishable representation.
type CanonicalSpec struct {
	SchemaVersion string
	Document      json.RawMessage
	Digest        string
}
