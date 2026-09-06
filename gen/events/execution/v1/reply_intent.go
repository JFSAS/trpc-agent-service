// Code generated from api/events/execution/v1/reply-intent.schema.json; DO NOT EDIT.

// Package executionv1 contains transport DTOs, not Gateway or Worker domain entities.
package executionv1

// ReplyIntent is defined by the versioned execution JSON Schema.
type ReplyIntent struct {
	AdmissionID   string           `json:"admission_id"`
	Content       FinalTextContent `json:"content"`
	Deadline      string           `json:"deadline"`
	Execution     ReplyExecution   `json:"execution"`
	IntentID      string           `json:"intent_id"`
	Kind          string           `json:"kind"`
	RunID         string           `json:"run_id"`
	SchemaVersion int64            `json:"schema_version"`
	Sequence      int64            `json:"sequence"`
}

// FinalTextContent is defined by the versioned execution JSON Schema.
type FinalTextContent struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

// ReplyExecution is defined by the versioned execution JSON Schema.
type ReplyExecution struct {
	AttemptID    string `json:"attempt_id"`
	CompletionID string `json:"completion_id"`
	Generation   int64  `json:"generation"`
}
