package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	controleventsv1 "github.com/liuzengh/trpc-agent-service/api/events/control/v1"
	deploymentv1 "github.com/liuzengh/trpc-agent-service/api/schemas/deployment/v1"
)

func TestActualPublicationOutputSatisfiesClosedEventAndManifestSchemas(t *testing.T) {
	h := newHarness(t)
	created := h.mustCreate(t)
	result, err := h.service.PublishDeploymentRevision(context.Background(), PublishDeploymentCommand{
		TenantID: "tenant-1", DeploymentID: created.ID, ActorUserID: "owner",
		IdempotencyKey: "schema-publication", Input: deploymentInput(),
	})
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const manifestURL = "https://jfsas.dev/schemas/deployment/v1/runtime-manifest.schema.json"
	const eventURL = "https://jfsas.dev/events/control/v1/runtime-manifest-published.schema.json"
	for location, raw := range map[string][]byte{
		manifestURL: deploymentv1.ManifestSchema,
		eventURL:    controleventsv1.RuntimeManifestPublishedSchema,
	} {
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(location, document); err != nil {
			t.Fatal(err)
		}
	}
	for location, raw := range map[string]json.RawMessage{
		eventURL:    h.store.lastEvent.Payload,
		manifestURL: mustJSON(t, result.Published.Manifest),
	} {
		schema, err := compiler.Compile(location)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(document); err != nil {
			t.Fatalf("actual publication violates %s: %v", location, err)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
