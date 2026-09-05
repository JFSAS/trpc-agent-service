# Control events v1

This namespace owns versioned Control publication events that cross the
Control/Execution boundary.

## `RuntimeManifestPublished.v1`

`runtime-manifest-published.schema.json` is the closed event contract created
with a successful Deployment Revision publication. The PostgreSQL publication
transaction persists the event in `control_outbox` with status `PENDING`; no
Relay or JetStream delivery is currently wired. It carries the complete,
immutable RuntimeManifest envelope and canonical content required by trusted
runtime projection consumers. It carries internal credential identity,
purpose, and audience metadata, but never credential values, ciphertext,
mutable credential state, or request headers.

The event, manifest envelope, and manifest content repeat only identities that
consumers must validate. Their Tenant, Deployment Revision, revision number,
publication time, and content digest must agree. JSON Schema checks structure;
schema and producer/domain tests additionally enforce these cross-field invariants.

`trace_context` is optional and restricted to normalized W3C `traceparent` and
bounded `tracestate`. Arbitrary baggage and inbound headers are not events.

Transport subject, stream, retention, relay state, and consumer retry policy
belong to deployment/runtime configuration rather than this payload schema.
The reserved V1 subject is `control.runtime-manifest.published.v1`; reserving it
does not claim that a stream, publisher, consumer, or runtime projection exists.
