# AgentSpec Schema v1

This directory owns the immutable public AgentSpec V1 protocol:

- [`agent-spec.schema.json`](agent-spec.schema.json) is the Draft 2020-12 JSON
  Schema.
- `embed.go` exposes the same schema to Go verification without copying it.
- `examples/valid/` and `examples/invalid/` are executable compatibility
  fixtures shared by schema and domain-validator tests.

The accepted ownership, publication boundary, validation levels, diagnostic
codes, and canonicalization rules are documented in the
[Agent subdomain design](../../../../docs/architecture-next/control-api/agent.md)
and
[AgentSpec V1 design](../../../../docs/architecture-next/control-api/agent-spec.md).

V1 accepts only `llm`, `sequence`, `parallel`, and bounded `loop` nodes. It does
not expose credentials, concrete Runtime Profile bindings, editor state, Graph
expressions, or tRPC-Agent-Go runtime options.
