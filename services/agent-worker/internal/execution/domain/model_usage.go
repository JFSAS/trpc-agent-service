package domain

import "encoding/json"

// One fixed single-LLM V1 call belongs to one Attempt. A new Attempt has a new
// operation ID; this identity is not permission to repeat a physical request.
func ModelOperationID(g Grant) string {
	return StableID("modelop", g.Run.Request.Route.TenantID+"\x00"+g.Run.Request.RunID+"\x00"+g.AttemptID+"\x00single-llm-v1")
}
func ModelUsageDigest(g Grant, r RuntimeResult) (string, error) {
	const max int64 = 9007199254740991
	if !r.UsageKnown || r.InputTokens < 0 || r.OutputTokens < 0 || r.TotalTokens < 0 || r.InputTokens > max || r.OutputTokens > max || r.TotalTokens > max || r.InputTokens+r.OutputTokens != r.TotalTokens || g.AttemptID == "" || !DigestValid(g.Run.Request.RunDigest) || !DigestValid(g.Run.Request.Route.ManifestDigest) || !g.Parent.Valid() {
		return "", ErrInvalid
	}
	b, _ := json.Marshal([]any{"model-usage-v1", ModelOperationID(g), g.Run.Request.RunDigest, g.Run.Request.Route.ManifestDigest, g.Parent, r.InputTokens, r.OutputTokens, r.TotalTokens})
	return Digest(b), nil
}
