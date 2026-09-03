package domain_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	runtimeprofilev1 "github.com/liuzengh/trpc-agent-service/api/schemas/runtimeprofile/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

func TestValidFixturesPassPublicSchemaDomainAndTypedRoundTrip(t *testing.T) {
	schema := compilePublicSchema(t)
	files, err := filepath.Glob(fixturePath(t, "valid", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no valid RuntimeProfileSpec fixtures found")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			document := readFixture(t, file)
			validateWithPublicSchema(t, schema, document, true)

			canonical, report := domain.ValidateForPublication(document, 7)
			if !report.Valid {
				t.Fatalf("domain validation report = %#v", report)
			}
			if report.SchemaVersion != domain.SchemaVersionV1 || report.DraftRevision != 7 ||
				canonical.SchemaVersion != domain.SchemaVersionV1 ||
				!strings.HasPrefix(canonical.Digest, "sha256:") {
				t.Fatalf("canonical/report = %#v / %#v", canonical, report)
			}
			validateWithPublicSchema(t, schema, canonical.Document, true)

			var typed domain.Spec
			if err := json.Unmarshal(canonical.Document, &typed); err != nil {
				t.Fatalf("decode typed RuntimeProfileSpec: %v", err)
			}
			roundTrip, err := json.Marshal(typed)
			if err != nil {
				t.Fatalf("marshal typed RuntimeProfileSpec: %v", err)
			}
			validateWithPublicSchema(t, schema, roundTrip, true)
			recanonical, roundTripReport := domain.ValidateForPublication(roundTrip, 7)
			if !roundTripReport.Valid || recanonical.Digest != canonical.Digest ||
				!bytes.Equal(recanonical.Document, canonical.Document) {
				t.Fatalf("typed round trip changed canonical form: %#v / %s", roundTripReport, roundTrip)
			}
		})
	}
}

func TestInvalidFixturesReturnStableDiagnostics(t *testing.T) {
	tests := map[string]struct {
		code         string
		pointer      string
		resourceKind string
		resourceKey  string
	}{
		"duplicate-capability.json": {
			"RUNTIME_PROFILE_SPEC_DUPLICATE_CAPABILITY", "/models/primary/capabilities/1", "model", "primary",
		},
		"invalid-secret-ref.json": {
			"RUNTIME_PROFILE_SPEC_SECRET_REF_INVALID", "/storage/conversation_state/dsn_ref", "storage", "conversation_state",
		},
		"missing-chat.json": {
			"RUNTIME_PROFILE_SPEC_CAPABILITY_KIND_MISMATCH", "/models/primary/capabilities", "model", "primary",
		},
		"plaintext-secret.json": {
			"RUNTIME_PROFILE_SPEC_SENSITIVE_FIELD", "/models/primary/api_key", "model", "primary",
		},
		"unknown-field.json": {
			"RUNTIME_PROFILE_SPEC_UNKNOWN_FIELD", "/models/primary/provider_options", "model", "primary",
		},
		"unsupported-kind.json": {
			"RUNTIME_PROFILE_SPEC_UNSUPPORTED_KIND", "/models/primary/kind", "model", "primary",
		},
		"url-query.json": {
			"RUNTIME_PROFILE_SPEC_INVALID_URL", "/tools/web_search/server_url", "tool", "web_search",
		},
		"url-userinfo.json": {
			"RUNTIME_PROFILE_SPEC_INVALID_URL", "/models/primary/base_url", "model", "primary",
		},
	}
	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			document := readFixture(t, fixturePath(t, "invalid", name))
			_, report := domain.ValidateForPublication(document, 2)
			if report.Valid {
				t.Fatal("invalid fixture was accepted")
			}
			diagnostic, ok := findDiagnostic(report, expected.code)
			if !ok || diagnostic.Pointer != expected.pointer ||
				pointerValue(diagnostic.ResourceKind) != expected.resourceKind ||
				pointerValue(diagnostic.ResourceKey) != expected.resourceKey {
				t.Fatalf("diagnostics = %#v, want %s at %s for %s/%s",
					report.Diagnostics, expected.code, expected.pointer,
					expected.resourceKind, expected.resourceKey)
			}
		})
	}
}

func TestPublicSchemaAndDomainOnlyInvalidFixturesHaveExplicitBoundary(t *testing.T) {
	schema := compilePublicSchema(t)
	schemaRejects := map[string]bool{
		"duplicate-capability.json": true,
		"invalid-secret-ref.json":   true,
		"plaintext-secret.json":     true,
		"unknown-field.json":        true,
		"unsupported-kind.json":     true,
		"missing-chat.json":         false,
		"url-query.json":            false,
		"url-userinfo.json":         false,
	}
	for name, wantReject := range schemaRejects {
		t.Run(name, func(t *testing.T) {
			document := readFixture(t, fixturePath(t, "invalid", name))
			validateWithPublicSchema(t, schema, document, !wantReject)
			_, report := domain.ValidateForPublication(document, 1)
			if report.Valid {
				t.Fatal("combined L0/L1/L2 validator accepted invalid fixture")
			}
		})
	}
}

func TestDraftStorageRunsOnlyL0(t *testing.T) {
	for name, document := range map[string]json.RawMessage{
		"empty object":      json.RawMessage(`{}`),
		"incomplete object": json.RawMessage(`{"models":null}`),
		"unknown kind":      json.RawMessage(`{"models":{"x":{"kind":"future"}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			report := domain.ValidateDraftForStorage(document, 4)
			if !report.Valid || report.DraftRevision != 4 {
				t.Fatalf("incomplete draft report = %#v", report)
			}
		})
	}

	tests := map[string]struct {
		document json.RawMessage
		code     string
		pointer  string
	}{
		"empty document": {nil, "RUNTIME_PROFILE_SPEC_DOCUMENT_REQUIRED", ""},
		"duplicate": {
			json.RawMessage(`{"models":{},"models":{}}`),
			"RUNTIME_PROFILE_SPEC_DUPLICATE_KEY", "/models",
		},
		"nested sensitive": {
			json.RawMessage(`{"models":{"primary":{"API_KEY":"plaintext"}}}`),
			"RUNTIME_PROFILE_SPEC_SENSITIVE_FIELD", "/models/primary/API_KEY",
		},
		"non object": {json.RawMessage(`[]`), "RUNTIME_PROFILE_SPEC_DOCUMENT_REQUIRED", ""},
		"malformed":  {json.RawMessage(`{"x":}`), "RUNTIME_PROFILE_SPEC_INVALID_JSON", ""},
		"two values": {json.RawMessage(`{} {}`), "RUNTIME_PROFILE_SPEC_INVALID_JSON", ""},
		"lone surrogate": {
			json.RawMessage(`{"x":"\ud800"}`), "RUNTIME_PROFILE_SPEC_INVALID_JSON", "",
		},
		"invalid utf8": {
			json.RawMessage{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
			"RUNTIME_PROFILE_SPEC_INVALID_JSON", "",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			report := domain.ValidateDraftForStorage(test.document, 1)
			diagnostic, ok := findDiagnostic(report, test.code)
			if report.Valid || !ok || diagnostic.Pointer != test.pointer {
				t.Fatalf("report = %#v, want %s at %q", report, test.code, test.pointer)
			}
		})
	}
}

func TestDocumentSizeLimitIsInclusive(t *testing.T) {
	atLimit := append(json.RawMessage(`{}`), bytes.Repeat([]byte(" "), domain.MaxDocumentBytes-2)...)
	if report := domain.ValidateDraftForStorage(atLimit, 1); !report.Valid {
		t.Fatalf("document at limit report = %#v", report)
	}
	overLimit := append(atLimit, ' ')
	report := domain.ValidateDraftForStorage(overLimit, 1)
	if report.Valid || !hasDiagnostic(report, "RUNTIME_PROFILE_SPEC_DOCUMENT_TOO_LARGE") {
		t.Fatalf("document over limit report = %#v", report)
	}
}

func TestCanonicalGoldenDigestAndSetNormalization(t *testing.T) {
	document := readFixture(t, fixturePath(t, "valid", "model-tool.json"))
	canonical, report := domain.ValidateForPublication(document, 1)
	if !report.Valid {
		t.Fatalf("report = %#v", report)
	}
	const wantDocument = `{"knowledge":{},"models":{"primary":{"api_key_ref":"model-provider-primary","base_url":"https://model.example.com/v1","capabilities":["chat","tool_call"],"kind":"openai_compatible","model":"gpt-5.4-mini"}},"schema_version":"v1","storage":{},"tools":{"web_search":{"auth":{"kind":"bearer","secret_ref":"tool-search-credential"},"capability":"web.search","kind":"mcp_streamable_http","server_url":"https://tools.example.com/mcp","tool_name":"search","toolset_name":"internet"}}}`
	const wantDigest = "sha256:c5a1eac3ab02e184f738e89d3362b8e9f9cdc5acc8a7a5d1767daba1dfbe1a5c"
	if string(canonical.Document) != wantDocument || canonical.Digest != wantDigest {
		t.Fatalf("canonical = %s / %s", canonical.Digest, canonical.Document)
	}

	reversed := bytes.Replace(document,
		[]byte(`["chat", "tool_call"]`), []byte(`["tool_call", "chat"]`), 1)
	reordered, reorderedReport := domain.ValidateForPublication(reversed, 1)
	if !reorderedReport.Valid || reordered.Digest != canonical.Digest ||
		!bytes.Equal(reordered.Document, canonical.Document) {
		t.Fatalf("capability set order changed canonical output: %#v / %s", reorderedReport, reordered.Document)
	}
}

func TestCanonicalizationNormalizesObjectOrderAndMathematicalIntegers(t *testing.T) {
	minimalA := json.RawMessage(`{"schema_version":"v1","models":{},"tools":{},"knowledge":{},"storage":{}}`)
	minimalB := json.RawMessage(`{"storage":{},"knowledge":{},"tools":{},"models":{},"schema_version":"v1"}`)
	first, firstReport := domain.ValidateForPublication(minimalA, 1)
	second, secondReport := domain.ValidateForPublication(minimalB, 1)
	if !firstReport.Valid || !secondReport.Valid || first.Digest != second.Digest ||
		!bytes.Equal(first.Document, second.Document) {
		t.Fatalf("object order changed canonical output: %#v / %#v", first, second)
	}

	original := readFixture(t, fixturePath(t, "valid", "knowledge-storage.json"))
	equivalent := bytes.Replace(original, []byte(`"port": 6334`), []byte(`"port": 6.334e3`), 1)
	equivalent = bytes.Replace(equivalent, []byte(`"dimensions": 1536`), []byte(`"dimensions": 1536.0`), 1)
	want, wantReport := domain.ValidateForPublication(original, 1)
	got, gotReport := domain.ValidateForPublication(equivalent, 1)
	if !wantReport.Valid || !gotReport.Valid || want.Digest != got.Digest ||
		!bytes.Equal(want.Document, got.Document) {
		t.Fatalf("mathematical integer normalization failed: %#v / %#v", want, got)
	}
	if !bytes.Contains(got.Document, []byte(`"port":6334`)) ||
		!bytes.Contains(got.Document, []byte(`"dimensions":1536`)) {
		t.Fatalf("canonical integer fields = %s", got.Document)
	}

	nonInteger := bytes.Replace(original, []byte(`"dimensions": 1536`),
		[]byte(`"dimensions": 1536.0000000000000000001`), 1)
	_, nonIntegerReport := domain.ValidateForPublication(nonInteger, 1)
	if nonIntegerReport.Valid || !hasDiagnosticAt(
		nonIntegerReport, "RUNTIME_PROFILE_SPEC_INVALID_TYPE",
		"/knowledge/product_docs/embedding/dimensions",
	) {
		t.Fatalf("exact non-integer report = %#v", nonIntegerReport)
	}
}

func TestDiagnosticsAreStableContextualAndRequiredNullable(t *testing.T) {
	validReport := domain.ValidateDraftForStorage(json.RawMessage(`{}`), 11)
	encoded, err := json.Marshal(validReport)
	if err != nil {
		t.Fatal(err)
	}
	const wantValid = `{"valid":true,"schema_version":"","draft_revision":11,"diagnostics":[]}`
	if string(encoded) != wantValid {
		t.Fatalf("valid report JSON = %s", encoded)
	}

	invalidReport := domain.ValidateDraftForStorage(json.RawMessage(`[]`), 12)
	encoded, err = json.Marshal(invalidReport)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"resource_kind":null`)) ||
		!bytes.Contains(encoded, []byte(`"resource_key":null`)) {
		t.Fatalf("nullable diagnostic fields are missing: %s", encoded)
	}

	document := json.RawMessage(`{
		"schema_version":"v1",
		"models":{
			"z":{"kind":"openai_compatible","model":"m","base_url":"https://example.com","api_key_ref":"key","capabilities":["chat"],"z_extra":true},
			"a/b":{"kind":"openai_compatible","model":"m","base_url":"https://example.com","api_key_ref":"key","capabilities":["chat"],"a_extra":true}
		},
		"tools":{},"knowledge":{},"storage":{}
	}`)
	_, first := domain.ValidateForPublication(document, 3)
	_, second := domain.ValidateForPublication(document, 3)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("same input produced unstable diagnostics:\n%s\n%s", firstJSON, secondJSON)
	}
	pointers := make([]string, 0, len(first.Diagnostics))
	for _, diagnostic := range first.Diagnostics {
		pointers = append(pointers, diagnostic.Pointer)
	}
	if !sort.StringsAreSorted(pointers) {
		t.Fatalf("diagnostic pointers are not sorted: %v", pointers)
	}
	diagnostic, ok := findDiagnosticAt(first, "RUNTIME_PROFILE_SPEC_INVALID_IDENTIFIER", "/models/a~1b")
	if !ok || pointerValue(diagnostic.ResourceKind) != "model" ||
		pointerValue(diagnostic.ResourceKey) != "a/b" {
		t.Fatalf("escaped resource context = %#v", diagnostic)
	}
}

func TestHTTPURLRules(t *testing.T) {
	valid := []string{
		"http://localhost",
		"https://example.com/v1",
		"HTTPS://example.com/v1",
		"https://[2001:db8::1]:8443/v1",
		"https://example.com/v1%3Fencoded",
	}
	for _, endpoint := range valid {
		t.Run("valid "+endpoint, func(t *testing.T) {
			_, report := domain.ValidateForPublication(modelDocument(endpoint, "model-key", []string{"chat"}), 1)
			if !report.Valid {
				t.Fatalf("valid URL report = %#v", report)
			}
		})
	}

	invalid := []string{
		"ftp://example.com/v1",
		"/relative/v1",
		"https://user:password@example.com/v1",
		"https://example.com/v1?token=x",
		"https://example.com/v1?",
		"https://example.com/v1#part",
		"https://example.com/v1#",
		"https:///v1",
		"https://:443/v1",
		"https://exa mple.com/v1",
	}
	for _, endpoint := range invalid {
		t.Run("invalid "+endpoint, func(t *testing.T) {
			_, report := domain.ValidateForPublication(modelDocument(endpoint, "model-key", []string{"chat"}), 1)
			if report.Valid || !hasDiagnosticAt(
				report, "RUNTIME_PROFILE_SPEC_INVALID_URL", "/models/primary/base_url",
			) {
				t.Fatalf("invalid URL report = %#v", report)
			}
		})
	}
}

func TestSecretRefRulesAtEverySupportedLocation(t *testing.T) {
	validRefs := []string{"a", "a" + strings.Repeat("0", 63), "model-key_1"}
	for _, ref := range validRefs {
		t.Run("valid "+ref, func(t *testing.T) {
			_, report := domain.ValidateForPublication(modelDocument("https://example.com/v1", ref, []string{"chat"}), 1)
			if !report.Valid {
				t.Fatalf("valid SecretRef report = %#v", report)
			}
		})
	}

	invalidRefs := []string{"A", "1key", "a" + strings.Repeat("0", 64), "vault://key", "key.name"}
	for _, ref := range invalidRefs {
		t.Run("invalid "+ref, func(t *testing.T) {
			_, report := domain.ValidateForPublication(modelDocument("https://example.com/v1", ref, []string{"chat"}), 1)
			if report.Valid || !hasDiagnosticAt(
				report, "RUNTIME_PROFILE_SPEC_SECRET_REF_INVALID", "/models/primary/api_key_ref",
			) {
				t.Fatalf("invalid SecretRef report = %#v", report)
			}
		})
	}

	tests := []struct {
		name    string
		fixture string
		mutate  func(map[string]any)
		pointer string
	}{
		{"tool bearer", "model-tool.json", func(root map[string]any) {
			root["tools"].(map[string]any)["web_search"].(map[string]any)["auth"].(map[string]any)["secret_ref"] = "INVALID"
		}, "/tools/web_search/auth/secret_ref"},
		{"qdrant", "knowledge-storage.json", func(root map[string]any) {
			root["knowledge"].(map[string]any)["product_docs"].(map[string]any)["qdrant_api_key_ref"] = "INVALID"
		}, "/knowledge/product_docs/qdrant_api_key_ref"},
		{"embedding", "knowledge-storage.json", func(root map[string]any) {
			root["knowledge"].(map[string]any)["product_docs"].(map[string]any)["embedding"].(map[string]any)["api_key_ref"] = "INVALID"
		}, "/knowledge/product_docs/embedding/api_key_ref"},
		{"storage", "knowledge-storage.json", func(root map[string]any) {
			root["storage"].(map[string]any)["conversation_state"].(map[string]any)["dsn_ref"] = "INVALID"
		}, "/storage/conversation_state/dsn_ref"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(readFixture(t, fixturePath(t, "valid", test.fixture)), &root); err != nil {
				t.Fatal(err)
			}
			test.mutate(root)
			document, _ := json.Marshal(root)
			_, report := domain.ValidateForPublication(document, 1)
			if report.Valid || !hasDiagnosticAt(report, "RUNTIME_PROFILE_SPEC_SECRET_REF_INVALID", test.pointer) {
				t.Fatalf("SecretRef location report = %#v", report)
			}
		})
	}
}

func TestCapabilityRulesAndDerivedCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		document json.RawMessage
		valid    bool
		code     string
		pointer  string
	}{
		{"chat", modelDocument("https://example.com", "model-key", []string{"chat"}), true, "", ""},
		{"chat and tool", modelDocument("https://example.com", "model-key", []string{"tool_call", "chat"}), true, "", ""},
		{"missing chat", modelDocument("https://example.com", "model-key", []string{"tool_call"}), false,
			"RUNTIME_PROFILE_SPEC_CAPABILITY_KIND_MISMATCH", "/models/primary/capabilities"},
		{"unsupported model capability", modelDocument("https://example.com", "model-key", []string{"chat", "vision"}), false,
			"RUNTIME_PROFILE_SPEC_CAPABILITY_KIND_MISMATCH", "/models/primary/capabilities/1"},
		{"duplicate model capability", modelDocument("https://example.com", "model-key", []string{"chat", "chat"}), false,
			"RUNTIME_PROFILE_SPEC_DUPLICATE_CAPABILITY", "/models/primary/capabilities/1"},
		{"unsupported tool capability", toolDocument("files.read"), false,
			"RUNTIME_PROFILE_SPEC_CAPABILITY_KIND_MISMATCH", "/tools/search/capability"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, report := domain.ValidateForPublication(test.document, 1)
			if report.Valid != test.valid {
				t.Fatalf("valid = %v, report = %#v", report.Valid, report)
			}
			if !test.valid && !hasDiagnosticAt(report, test.code, test.pointer) {
				t.Fatalf("report = %#v, want %s at %s", report, test.code, test.pointer)
			}
		})
	}

	model := domain.ModelResource{Capabilities: []string{domain.CapabilityChat, domain.CapabilityToolCall}}
	modelCaps := model.ProvidedCapabilities()
	modelCaps[0] = "changed"
	if !reflect.DeepEqual(model.Capabilities, []string{domain.CapabilityChat, domain.CapabilityToolCall}) {
		t.Fatalf("ProvidedCapabilities aliases model state: %v", model.Capabilities)
	}
	if got := (domain.ToolResource{Capability: domain.CapabilityWebSearch}).ProvidedCapabilities(); !reflect.DeepEqual(got, []string{domain.CapabilityWebSearch}) {
		t.Fatalf("tool capabilities = %v", got)
	}
	if got := (domain.KnowledgeResource{}).ProvidedCapabilities(); !reflect.DeepEqual(got, []string{domain.CapabilityKnowledgeSearch}) {
		t.Fatalf("knowledge capabilities = %v", got)
	}
	if got := (domain.StorageResource{}).ProvidedCapabilities(); !reflect.DeepEqual(got, []string{
		domain.CapabilityStorageSession, domain.CapabilityStorageMemory,
	}) {
		t.Fatalf("storage capabilities = %v", got)
	}
}

func TestResourceMapLimitsAndExplicitNulls(t *testing.T) {
	tests := []struct {
		collection string
		maximum    int
		resource   func(int) any
	}{
		{"models", domain.MaxModelResources, func(i int) any {
			return map[string]any{"kind": "openai_compatible", "model": "m", "base_url": "https://example.com", "api_key_ref": "key", "capabilities": []any{"chat"}}
		}},
		{"tools", domain.MaxToolResources, func(i int) any {
			return map[string]any{"kind": "mcp_streamable_http", "server_url": "https://example.com/mcp", "toolset_name": "set", "tool_name": "tool", "auth": map[string]any{"kind": "none"}, "capability": "web.search"}
		}},
		{"knowledge", domain.MaxKnowledgeResources, func(i int) any {
			return map[string]any{"kind": "qdrant_openai", "host": "qdrant", "port": json.Number("6333"), "tls": false, "collection": "docs", "embedding": map[string]any{"model": "embedding", "base_url": "https://example.com", "api_key_ref": "key", "dimensions": json.Number("1536")}}
		}},
		{"storage", domain.MaxStorageResources, func(i int) any {
			return map[string]any{"kind": "postgres_state", "dsn_ref": "key"}
		}},
	}
	for _, test := range tests {
		t.Run(test.collection, func(t *testing.T) {
			root := emptySpecMap()
			resources := root[test.collection].(map[string]any)
			for i := 0; i <= test.maximum; i++ {
				resources[fmt.Sprintf("r%02d", i)] = test.resource(i)
			}
			document, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			_, report := domain.ValidateForPublication(document, 1)
			if report.Valid || !hasDiagnosticAt(report, "RUNTIME_PROFILE_SPEC_LIMIT_EXCEEDED", "/"+test.collection) {
				t.Fatalf("resource limit report = %#v", report)
			}
		})
	}

	for _, collection := range []string{"models", "tools", "knowledge", "storage"} {
		t.Run(collection+" null", func(t *testing.T) {
			root := emptySpecMap()
			root[collection] = nil
			document, _ := json.Marshal(root)
			_, report := domain.ValidateForPublication(document, 1)
			if report.Valid || !hasDiagnosticAt(report, "RUNTIME_PROFILE_SPEC_INVALID_TYPE", "/"+collection) {
				t.Fatalf("null collection report = %#v", report)
			}
		})
	}
}

func TestClosedObjectsAndToolAuthUnion(t *testing.T) {
	tests := []struct {
		name     string
		document json.RawMessage
		code     string
		pointer  string
	}{
		{"top level", json.RawMessage(`{"schema_version":"v1","models":{},"tools":{},"knowledge":{},"storage":{},"environment":"prod"}`),
			"RUNTIME_PROFILE_SPEC_UNKNOWN_FIELD", "/environment"},
		{"none auth secret", json.RawMessage(`{"schema_version":"v1","models":{},"tools":{"search":{"kind":"mcp_streamable_http","server_url":"https://example.com/mcp","toolset_name":"web","tool_name":"search","auth":{"kind":"none","secret_ref":"key"},"capability":"web.search"}},"knowledge":{},"storage":{}}`),
			"RUNTIME_PROFILE_SPEC_UNKNOWN_FIELD", "/tools/search/auth/secret_ref"},
		{"bearer missing secret", json.RawMessage(`{"schema_version":"v1","models":{},"tools":{"search":{"kind":"mcp_streamable_http","server_url":"https://example.com/mcp","toolset_name":"web","tool_name":"search","auth":{"kind":"bearer"},"capability":"web.search"}},"knowledge":{},"storage":{}}`),
			"RUNTIME_PROFILE_SPEC_REQUIRED_FIELD", "/tools/search/auth/secret_ref"},
		{"unknown auth", json.RawMessage(`{"schema_version":"v1","models":{},"tools":{"search":{"kind":"mcp_streamable_http","server_url":"https://example.com/mcp","toolset_name":"web","tool_name":"search","auth":{"kind":"basic"},"capability":"web.search"}},"knowledge":{},"storage":{}}`),
			"RUNTIME_PROFILE_SPEC_UNSUPPORTED_KIND", "/tools/search/auth/kind"},
		{"null auth", json.RawMessage(`{"schema_version":"v1","models":{},"tools":{"search":{"kind":"mcp_streamable_http","server_url":"https://example.com/mcp","toolset_name":"web","tool_name":"search","auth":null,"capability":"web.search"}},"knowledge":{},"storage":{}}`),
			"RUNTIME_PROFILE_SPEC_INVALID_TYPE", "/tools/search/auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, report := domain.ValidateForPublication(test.document, 1)
			if report.Valid || !hasDiagnosticAt(report, test.code, test.pointer) {
				t.Fatalf("closed shape report = %#v", report)
			}
		})
	}

	none := domain.ToolAuth{Kind: domain.AuthKindNone, SecretRef: "must-not-leak"}
	encoded, err := json.Marshal(none)
	if err != nil || string(encoded) != `{"kind":"none"}` {
		t.Fatalf("none auth JSON = %s / %v", encoded, err)
	}
	bearer := domain.ToolAuth{Kind: domain.AuthKindBearer, SecretRef: "tool-key"}
	encoded, err = json.Marshal(bearer)
	if err != nil || string(encoded) != `{"kind":"bearer","secret_ref":"tool-key"}` {
		t.Fatalf("bearer auth JSON = %s / %v", encoded, err)
	}
}

func compilePublicSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(runtimeprofilev1.Schema))
	if err != nil {
		t.Fatalf("decode public schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://jfsas.dev/schemas/runtimeprofile/v1/runtime-profile-spec.schema.json"
	if err := compiler.AddResource(location, document); err != nil {
		t.Fatalf("add public schema: %v", err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatalf("compile public schema: %v", err)
	}
	return schema
}

func validateWithPublicSchema(t *testing.T, schema *jsonschema.Schema, document []byte, wantValid bool) {
	t.Helper()
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(document))
	if err != nil {
		if wantValid {
			t.Fatalf("decode JSON instance: %v", err)
		}
		return
	}
	err = schema.Validate(instance)
	if wantValid && err != nil {
		t.Fatalf("public JSON Schema rejected document: %v", err)
	}
	if !wantValid && err == nil {
		t.Fatal("public JSON Schema accepted invalid document")
	}
}

func fixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	all := append([]string{"..", "..", "..", "..", "..", "api", "schemas", "runtimeprofile", "v1", "examples"}, parts...)
	return filepath.Join(all...)
}

func readFixture(t *testing.T, path string) json.RawMessage {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func modelDocument(baseURL, apiKeyRef string, capabilities []string) json.RawMessage {
	root := emptySpecMap()
	root["models"].(map[string]any)["primary"] = map[string]any{
		"kind": "openai_compatible", "model": "model",
		"base_url": baseURL, "api_key_ref": apiKeyRef,
		"capabilities": capabilities,
	}
	document, err := json.Marshal(root)
	if err != nil {
		panic(err)
	}
	return document
}

func toolDocument(capability string) json.RawMessage {
	root := emptySpecMap()
	root["tools"].(map[string]any)["search"] = map[string]any{
		"kind": "mcp_streamable_http", "server_url": "https://example.com/mcp",
		"toolset_name": "web", "tool_name": "search",
		"auth": map[string]any{"kind": "none"}, "capability": capability,
	}
	document, err := json.Marshal(root)
	if err != nil {
		panic(err)
	}
	return document
}

func emptySpecMap() map[string]any {
	return map[string]any{
		"schema_version": "v1",
		"models":         map[string]any{},
		"tools":          map[string]any{},
		"knowledge":      map[string]any{},
		"storage":        map[string]any{},
	}
}

func hasDiagnostic(report domain.ValidationReport, code string) bool {
	_, ok := findDiagnostic(report, code)
	return ok
}

func hasDiagnosticAt(report domain.ValidationReport, code, pointer string) bool {
	_, ok := findDiagnosticAt(report, code, pointer)
	return ok
}

func findDiagnostic(report domain.ValidationReport, code string) (domain.Diagnostic, bool) {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code {
			return diagnostic, true
		}
	}
	return domain.Diagnostic{}, false
}

func findDiagnosticAt(report domain.ValidationReport, code, pointer string) (domain.Diagnostic, bool) {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code && diagnostic.Pointer == pointer {
			return diagnostic, true
		}
	}
	return domain.Diagnostic{}, false
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
