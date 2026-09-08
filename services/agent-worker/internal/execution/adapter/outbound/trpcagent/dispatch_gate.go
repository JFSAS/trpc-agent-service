package trpcagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

var ErrModelAdmission = errors.New("model request admission failed")

// ModelCall is the actual SDK request, not an input-only token estimate. Body is
// sensitive transient data for trusted admission/token accounting; never log it.
// A digest proves byte identity, not token count, policy freshness or cost bound.
type ModelCall = domain.ModelCall

type ModelCallGate interface {
	AuthorizeModelCall(context.Context, ModelCall) error
}

// dispatchGate is attempt-local and single-use, including rejection. It blocks
// redirect/retry reuse rather than charging multiple HTTP requests as one call.
type dispatchGate struct {
	owner   ModelCallGate
	request Request
	maximum int64
	used    atomic.Bool
}

const maxModelRequestBytes = 8 * 1024 * 1024

func (g *dispatchGate) check(r *http.Request) error {
	if !g.used.CompareAndSwap(false, true) || r.Context().Err() != nil || r.Method != http.MethodPost || r.Body == nil || r.URL.String() != strings.TrimRight(g.request.Model.Endpoint, "/")+"/chat/completions" {
		return ErrModelAdmission
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxModelRequestBytes+1))
	closeErr := r.Body.Close()
	if err != nil || closeErr != nil || len(body) > maxModelRequestBytes {
		return ErrModelAdmission
	}
	// Restore exactly the bytes inspected. The gate receives a detached copy and
	// may not mutate it to authorize content different from the outbound request.
	r.Body = io.NopCloser(bytes.NewReader(body))
	var shape struct {
		Model   string `json:"model"`
		Maximum *int64 `json:"max_completion_tokens"`
	}
	if json.Unmarshal(body, &shape) != nil || shape.Model != g.request.Model.Name || shape.Maximum == nil || *shape.Maximum != g.maximum {
		return ErrModelAdmission
	}
	sum := sha256.Sum256(body)
	detached := bytes.Clone(body)
	call := ModelCall{TenantID: g.request.TenantID, SessionID: g.request.SessionID, RunID: g.request.RunID, AttemptID: g.request.AttemptID, Endpoint: r.URL.String(), Model: shape.Model, MaxOutputTokens: g.maximum, RequestDigest: "sha256:" + hex.EncodeToString(sum[:]), Body: detached}
	err = g.owner.AuthorizeModelCall(r.Context(), call)
	unchanged := bytes.Equal(body, detached)
	clear(detached)
	if err != nil || !unchanged || r.Context().Err() != nil {
		return ErrModelAdmission
	}
	return nil
}
