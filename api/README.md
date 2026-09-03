# Versioned protocols

This directory contains source protocol definitions shared across workloads.
It may contain OpenAPI documents, JSON Schema, and versioned event schemas. It
must not contain domain entities, repositories, or handwritten shared business
logic.

Current protocol namespaces:

- `openapi/control/v1`: the implemented Control API contract, including
  Identity, Admin, Tenant, Agent V1, and Runtime Profile V1.
- `schemas/agentspec/v1`: the frozen AgentSpec V1 JSON Schema and examples.
- `schemas/runtimeprofile/v1`: the frozen RuntimeProfileSpec V1 JSON Schema
  and positive/negative examples.
- `events/control/v1`: reserved for versioned control-plane events; no event
  contract is implemented yet.
- `events/execution/v1`: reserved for versioned execution-plane events; no
  event contract is implemented yet.
