package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const runtimeProfileSpecV1 = `{
  "schema_version":"v1",
  "models":{
    "primary":{
      "kind":"openai_compatible",
      "model":"gpt-4o-mini",
      "base_url":"https://api.openai.com/v1",
      "api_key_ref":"openai-primary",
      "capabilities":["tool_call","chat"]
    }
  },
  "tools":{
    "search":{
      "kind":"mcp_streamable_http",
      "server_url":"https://mcp.example.com/rpc",
      "toolset_name":"web",
      "tool_name":"search",
      "auth":{"kind":"bearer","secret_ref":"mcp-search"},
      "capability":"web.search"
    }
  },
  "knowledge":{
    "docs":{
      "kind":"qdrant_openai",
      "host":"qdrant.internal",
      "port":6334,
      "tls":true,
      "collection":"product_docs",
      "qdrant_api_key_ref":"qdrant-primary",
      "embedding":{
        "model":"text-embedding-3-small",
        "base_url":"https://api.openai.com/v1",
        "api_key_ref":"openai-primary",
        "dimensions":1536
      }
    }
  },
  "storage":{
    "state":{"kind":"postgres_state","dsn_ref":"agent-state-postgres"}
  }
}`

func testRuntimeProfileV1Lifecycle(
	t *testing.T,
	ctx context.Context,
	router http.Handler,
	pool *pgxpool.Pool,
	tenantID string,
	ownerCookie, memberCookie, outsiderCookie *http.Cookie,
) {
	t.Helper()
	base := "/v1/tenants/" + tenantID + "/runtime-profiles"
	var created struct {
		Profile struct {
			ID                   string `json:"id"`
			Name                 string `json:"name"`
			LatestRevisionNumber *int64 `json:"latest_revision_number"`
		} `json:"profile"`
		Draft struct {
			Revision int64           `json:"revision"`
			Spec     json.RawMessage `json:"spec"`
		} `json:"draft"`
	}
	request(t, router, http.MethodPost, base, ownerCookie,
		`{"name":"Production Runtime","description":"Reusable runtime resources"}`,
		http.StatusCreated, &created)
	if created.Profile.ID == "" || created.Profile.Name != "Production Runtime" ||
		created.Profile.LatestRevisionNumber != nil || created.Draft.Revision != 1 ||
		string(created.Draft.Spec) != `{}` {
		t.Fatalf("created Runtime Profile = %#v", created)
	}
	profilePath := base + "/" + created.Profile.ID

	var page struct {
		RuntimeProfiles []json.RawMessage `json:"runtime_profiles"`
		Total           int               `json:"total"`
	}
	request(t, router, http.MethodGet, base+"?offset=0&limit=20", ownerCookie, "",
		http.StatusOK, &page)
	if page.Total != 1 || len(page.RuntimeProfiles) != 1 {
		t.Fatalf("Runtime Profile page = %#v", page)
	}
	request(t, router, http.MethodGet, profilePath, ownerCookie, "", http.StatusOK, nil)
	request(t, router, http.MethodGet, profilePath, memberCookie, "", http.StatusOK, nil)
	request(t, router, http.MethodPatch, profilePath, ownerCookie,
		`{"name":"Production Runtime V1"}`, http.StatusOK, nil)
	request(t, router, http.MethodGet, profilePath+"/draft", ownerCookie, "", http.StatusOK, nil)

	var incompleteDraft struct {
		Revision int64 `json:"revision"`
	}
	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		`{"expected_revision":1,"spec":{"models":{}}}`,
		http.StatusOK, &incompleteDraft)
	if incompleteDraft.Revision != 2 {
		t.Fatalf("incomplete Profile Draft revision = %d", incompleteDraft.Revision)
	}
	var invalidReport struct {
		Valid         bool  `json:"valid"`
		DraftRevision int64 `json:"draft_revision"`
	}
	request(t, router, http.MethodPost, profilePath+"/draft/validate", ownerCookie,
		`{"expected_revision":2}`, http.StatusOK, &invalidReport)
	if invalidReport.Valid || invalidReport.DraftRevision != 2 {
		t.Fatalf("invalid Runtime Profile report = %#v", invalidReport)
	}
	request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":2}`, http.StatusUnprocessableEntity, nil)

	saveBody := runtimeProfileDraftBody(t, 2, runtimeProfileSpecV1)
	var validDraft struct {
		Revision int64 `json:"revision"`
	}
	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		saveBody, http.StatusOK, &validDraft)
	if validDraft.Revision != 3 {
		t.Fatalf("valid Profile Draft revision = %d", validDraft.Revision)
	}
	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		saveBody, http.StatusConflict, nil)
	var validReport struct {
		Valid bool `json:"valid"`
	}
	request(t, router, http.MethodPost, profilePath+"/draft/validate", ownerCookie,
		`{"expected_revision":3}`, http.StatusOK, &validReport)
	if !validReport.Valid {
		t.Fatal("valid RuntimeProfileSpec was rejected")
	}

	var firstPublish runtimeProfilePublishResponse
	firstRecorder := request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":3}`, http.StatusCreated, &firstPublish)
	assertRuntimeProfileRevision(t, firstPublish, 1, 3)
	assertPublishHasNoValidation(t, firstRecorder)

	var immediateRetry runtimeProfilePublishResponse
	retryRecorder := request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":3}`, http.StatusOK, &immediateRetry)
	if immediateRetry.Revision.ID != firstPublish.Revision.ID {
		t.Fatalf("immediate idempotent Revision ID = %q, want %q",
			immediateRetry.Revision.ID, firstPublish.Revision.ID)
	}
	assertPublishHasNoValidation(t, retryRecorder)

	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		`{"expected_revision":3,"spec":{"models":{}}}`, http.StatusOK, nil)
	var delayedRetry runtimeProfilePublishResponse
	request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":3}`, http.StatusOK, &delayedRetry)
	if delayedRetry.Revision.ID != firstPublish.Revision.ID {
		t.Fatalf("delayed idempotent Revision ID = %q, want %q",
			delayedRetry.Revision.ID, firstPublish.Revision.ID)
	}
	// Source Draft 2 failed validation and was never published, so it is stale.
	request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":2}`, http.StatusConflict, nil)

	var listed struct {
		Revisions []json.RawMessage `json:"revisions"`
		Total     int               `json:"total"`
	}
	request(t, router, http.MethodGet, profilePath+"/revisions", ownerCookie, "",
		http.StatusOK, &listed)
	if listed.Total != 1 || len(listed.Revisions) != 1 {
		t.Fatalf("Profile Revision page = %#v", listed)
	}
	var listedFields map[string]json.RawMessage
	if err := json.Unmarshal(listed.Revisions[0], &listedFields); err != nil {
		t.Fatal(err)
	}
	if _, exists := listedFields["spec"]; exists {
		t.Fatalf("Profile Revision list exposed canonical spec: %s", listed.Revisions[0])
	}
	var listedSummary struct {
		ID                  string `json:"id"`
		RevisionNumber      int64  `json:"revision_number"`
		SourceDraftRevision int64  `json:"source_draft_revision"`
		SchemaVersion       string `json:"schema_version"`
		SpecDigest          string `json:"spec_digest"`
	}
	if err := json.Unmarshal(listed.Revisions[0], &listedSummary); err != nil {
		t.Fatal(err)
	}
	if listedSummary.ID != firstPublish.Revision.ID || listedSummary.RevisionNumber != 1 ||
		listedSummary.SourceDraftRevision != 3 || listedSummary.SchemaVersion != "v1" ||
		listedSummary.SpecDigest != firstPublish.Revision.SpecDigest {
		t.Fatalf("Profile Revision summary = %#v", listedSummary)
	}
	var fetched runtimeProfileRevisionView
	request(t, router, http.MethodGet, profilePath+"/revisions/1", ownerCookie, "",
		http.StatusOK, &fetched)
	if fetched.SpecDigest != firstPublish.Revision.SpecDigest ||
		!bytes.Equal(fetched.Spec, firstPublish.Revision.Spec) {
		t.Fatalf("fetched immutable Profile Revision = %#v", fetched)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE runtime_profile_revisions SET spec_jsonb = '{}'::jsonb
		WHERE tenant_id = $1 AND profile_id = $2 AND revision_number = 1
	`, tenantID, created.Profile.ID); err == nil {
		t.Fatal("database allowed a ProfileRevision update")
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM runtime_profile_revisions
		WHERE tenant_id = $1 AND profile_id = $2 AND revision_number = 1
	`, tenantID, created.Profile.ID); err == nil {
		t.Fatal("database allowed a ProfileRevision delete")
	}

	// Pure Platform Operator identity has no implicit Tenant access.
	request(t, router, http.MethodGet, profilePath, outsiderCookie, "",
		http.StatusForbidden, nil)

	// A restricted Tenant member must rotate the temporary password first.
	var restrictedUser struct {
		ID string `json:"id"`
	}
	request(t, router, http.MethodPost, "/v1/admin/users", outsiderCookie,
		`{"username":"runtime-reviewer","display_name":"Runtime Reviewer","temporary_password":"Runtime-temp-pass-1234"}`,
		http.StatusCreated, &restrictedUser)
	request(t, router, http.MethodPost, "/v1/tenants/"+tenantID+"/members", ownerCookie,
		fmt.Sprintf(`{"user_id":%q}`, restrictedUser.ID), http.StatusCreated, nil)
	restrictedCookie := login(t, router, "runtime-reviewer", "Runtime-temp-pass-1234", true)
	request(t, router, http.MethodGet, profilePath, restrictedCookie, "",
		http.StatusForbidden, nil)

	// A failed latest-revision update must roll back the inserted Revision.
	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		runtimeProfileDraftBody(t, 4, runtimeProfileSpecV1), http.StatusOK, nil)
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_second_profile_revision() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.latest_revision_number > 1 THEN
				RAISE EXCEPTION 'injected latest revision update failure';
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER reject_second_profile_revision
		BEFORE UPDATE ON runtime_profiles
		FOR EACH ROW EXECUTE FUNCTION reject_second_profile_revision();
	`); err != nil {
		t.Fatal(err)
	}
	request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":5}`, http.StatusInternalServerError, nil)
	var revisionCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM runtime_profile_revisions
		WHERE tenant_id=$1 AND profile_id=$2
	`, tenantID, created.Profile.ID).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 1 {
		t.Fatalf("failed publication left %d Profile Revisions, want 1", revisionCount)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER reject_second_profile_revision ON runtime_profiles;
		DROP FUNCTION reject_second_profile_revision();
	`); err != nil {
		t.Fatal(err)
	}
	var secondPublish runtimeProfilePublishResponse
	request(t, router, http.MethodPost, profilePath+"/revisions", ownerCookie,
		`{"expected_revision":5}`, http.StatusCreated, &secondPublish)
	assertRuntimeProfileRevision(t, secondPublish, 2, 5)
	if secondPublish.Revision.SpecDigest != firstPublish.Revision.SpecDigest ||
		secondPublish.Revision.ID == firstPublish.Revision.ID {
		t.Fatal("same canonical content from a new Source Draft did not create a distinct Revision")
	}

	// Concurrent publication of one Source Draft creates exactly one row and ID.
	request(t, router, http.MethodPut, profilePath+"/draft", ownerCookie,
		runtimeProfileDraftBody(t, 5, runtimeProfileSpecV1), http.StatusOK, nil)
	concurrent := publishRuntimeProfileConcurrently(
		t, router, profilePath+"/revisions", ownerCookie, 6, 8,
	)
	createdResponses := 0
	var concurrentID string
	for _, result := range concurrent {
		if result.Status == http.StatusCreated {
			createdResponses++
		} else if result.Status != http.StatusOK {
			t.Fatalf("concurrent publication status/body = %d/%s", result.Status, result.Body)
		}
		if result.ID == "" {
			t.Fatalf("concurrent publication body = %s", result.Body)
		}
		if concurrentID == "" {
			concurrentID = result.ID
		} else if result.ID != concurrentID {
			t.Fatalf("concurrent publication IDs differ: %q != %q", result.ID, concurrentID)
		}
	}
	if createdResponses != 1 {
		t.Fatalf("concurrent created responses = %d, want 1", createdResponses)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM runtime_profile_revisions
		WHERE tenant_id=$1 AND profile_id=$2
	`, tenantID, created.Profile.ID).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 3 {
		t.Fatalf("Profile Revision count = %d, want 3", revisionCount)
	}

	// A failed initial Draft insert must roll back the Runtime Profile row.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_runtime_profile_draft() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected Runtime Profile Draft failure';
		END
		$$;
		CREATE TRIGGER reject_runtime_profile_draft
		BEFORE INSERT ON runtime_profile_drafts
		FOR EACH ROW EXECUTE FUNCTION reject_runtime_profile_draft();
	`); err != nil {
		t.Fatal(err)
	}
	request(t, router, http.MethodPost, base, ownerCookie,
		`{"name":"Must Roll Back"}`, http.StatusInternalServerError, nil)
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER reject_runtime_profile_draft ON runtime_profile_drafts;
		DROP FUNCTION reject_runtime_profile_draft();
	`); err != nil {
		t.Fatal(err)
	}
	var profileCount, draftCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*)::int FROM runtime_profiles WHERE tenant_id=$1", tenantID,
	).Scan(&profileCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		"SELECT count(*)::int FROM runtime_profile_drafts WHERE tenant_id=$1", tenantID,
	).Scan(&draftCount); err != nil {
		t.Fatal(err)
	}
	if profileCount != 1 || draftCount != 1 {
		t.Fatalf("Runtime Profile persistence counts = profile:%d draft:%d", profileCount, draftCount)
	}
}

type runtimeProfilePublishResponse struct {
	Revision runtimeProfileRevisionView `json:"revision"`
}

type runtimeProfileRevisionView struct {
	ID                  string          `json:"id"`
	RevisionNumber      int64           `json:"revision_number"`
	SourceDraftRevision int64           `json:"source_draft_revision"`
	Spec                json.RawMessage `json:"spec"`
	SpecDigest          string          `json:"spec_digest"`
}

func assertRuntimeProfileRevision(
	t *testing.T, response runtimeProfilePublishResponse, number, source int64,
) {
	t.Helper()
	if response.Revision.ID == "" || response.Revision.RevisionNumber != number ||
		response.Revision.SourceDraftRevision != source ||
		!strings.HasPrefix(response.Revision.SpecDigest, "sha256:") {
		t.Fatalf("published Profile Revision = %#v", response.Revision)
	}
}

func assertPublishHasNoValidation(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["validation"]; exists {
		t.Fatalf("successful publish leaked validation report: %s", recorder.Body.String())
	}
}

func runtimeProfileDraftBody(t *testing.T, expected int64, spec string) string {
	t.Helper()
	var raw json.RawMessage = []byte(spec)
	encoded, err := json.Marshal(map[string]any{
		"expected_revision": expected,
		"spec":              raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

type concurrentPublishResult struct {
	Status int
	ID     string
	Body   string
}

func publishRuntimeProfileConcurrently(
	t *testing.T,
	handler http.Handler,
	path string,
	cookie *http.Cookie,
	revision int64,
	workers int,
) []concurrentPublishResult {
	t.Helper()
	results := make(chan concurrentPublishResult, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			request := httptest.NewRequest(
				http.MethodPost, path,
				strings.NewReader(fmt.Sprintf(`{"expected_revision":%d}`, revision)),
			)
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(cookie)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			var response runtimeProfilePublishResponse
			_ = json.Unmarshal(recorder.Body.Bytes(), &response)
			results <- concurrentPublishResult{
				Status: recorder.Code,
				ID:     response.Revision.ID,
				Body:   recorder.Body.String(),
			}
		}()
	}
	group.Wait()
	close(results)
	collected := make([]concurrentPublishResult, 0, workers)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}
