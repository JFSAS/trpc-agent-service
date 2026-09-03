# RuntimeProfileSpec Schema v1

This directory owns the immutable public RuntimeProfileSpec V1 protocol:

- [`runtime-profile-spec.schema.json`](runtime-profile-spec.schema.json) is the
  strict Draft 2020-12 JSON Schema.
- `embed.go` exposes the same schema to Go validation without copying it.
- `examples/valid/` and `examples/invalid/` are executable compatibility
  fixtures shared by schema and domain-validator tests.

The top-level `schema_version`, `models`, `tools`, `knowledge`, and `storage`
fields are required. Each resource collection may be empty and accepts no more
than 16 models, 64 tools, 32 knowledge resources, or 16 storage resources.
Resource keys and secret references match `^[a-z][a-z0-9_-]{0,63}$`.

V1 is a closed discriminated union with one kind in each category:

| Category | Kind | Fixed boundary |
| --- | --- | --- |
| Model | `openai_compatible` | `chat`/`tool_call` allowlist; domain validation requires `chat` |
| Tool | `mcp_streamable_http` | `web.search`; `none` or typed bearer auth |
| Knowledge | `qdrant_openai` | fixed `knowledge.search`; typed Qdrant and OpenAI embedding fields |
| Storage | `postgres_state` | fixed `storage.session` and `storage.memory`; SecretRef-valued `dsn_ref` |

Knowledge and storage capabilities are derived from their resource kind and
therefore are not tenant-supplied document fields.

Every resource and nested value object rejects unknown fields. Credentials are
represented only by SecretRef fields; plaintext API keys, bearer tokens, DSNs,
headers, and arbitrary provider options are outside this protocol.

The schema applies the standard `uri` format and bounded lengths to endpoint
strings. Domain validation additionally requires HTTP or HTTPS, a non-empty
host, and no userinfo, query, or fragment. Domain validation also requires an
`openai_compatible` capability set to contain `chat`. Consequently
`missing-chat.json`, `url-query.json`, and `url-userinfo.json` can pass generic
schema validation but must fail the Runtime Profile domain validator.

The domain layer enforces the 512 KiB raw-document limit before parsing. It also
owns duplicate JSON-key rejection and recursive sensitive-field detection, so
the complete valid/invalid fixture contract is verified by combined transport,
schema, and domain tests rather than by schema validation alone.

The ownership, validation levels, canonicalization, and publication boundary
are documented in the
[Runtime Profile subdomain design](../../../../docs/architecture-next/control-api/runtime-profile.md)
and
[RuntimeProfileSpec V1 design](../../../../docs/architecture-next/control-api/runtime-profile-spec.md).
