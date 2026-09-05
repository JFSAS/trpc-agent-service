# Deployment protocols v1

This directory owns the current Deployment V1 source-selection and manifest
schemas.

- `deployment-input.schema.json` is the public, closed two-source input. It
  selects one immutable Agent Version and one immutable Runtime Profile
  Revision by owner ID and positive number.
- `runtime-manifest.schema.json` is the trusted internal execution snapshot.
  It may contain server-generated Profile Credential IDs needed by an
  authorized runtime consumer, but never credential values, ciphertext,
  nonces, dynamic status, or credential revisions.
- `runtime-manifest-view.schema.json` is the fixed public projection. It keeps
  non-secret configuration and replaces each internal credential descriptor
  with `credential_present`; the internal content digest cannot be recomputed
  from this redacted view.

All objects are closed. V1 has no Environment, latest/Draft selector, overlay,
or user-provided resource binding table. Deployment matches Agent requirements
to Profile resources by exact category and name.

Logical callable Entry IDs are exactly `tools/<resource_key>` and
`knowledge/<resource_key>`, for example `tools/search` and `knowledge/docs`.
They identify the selected logical resource, not a provider method name; do not
append a provider action such as `/search`. Provider callable-name generation
is separately fixed by the Worker Adapter contract.

Golden Manifest fixtures carry audience digests calculated from their concrete
resource destinations and a recomputed canonical content digest. Domain contract
tests validate every internal golden with `ValidateManifestContent`, compare its
exact public projection, and validate event-embedded manifests. The reverse
direction validates actual Compiler output against Manifest, View, and Event
schemas, with and without optional credentials.

The credential purposes in the internal Manifest exactly match the Profile
owner contract: `api_key`, `bearer_token`, `qdrant_api_key`,
`embedding_api_key`, and `dsn`. PostgreSQL storage fixes `host`, `port`,
`database`, `username`, and `sslmode` separately from the encrypted password.

`callable-name-v1.json` freezes the node-local Provider registration contract:
`fn_` plus the first 60 lowercase hexadecimal SHA-256 digits of the complete
logical EntryID, exactly 63 ASCII characters. Compiler and manifest validation
use the same pure resolver intended for the future Worker. Invalid or duplicate
entries and name collisions reject the whole node set; enumeration-order
suffixes and remote-name guessing are not part of V1.
