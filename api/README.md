# Versioned protocols

This directory contains source protocol definitions shared across workloads.
It may contain OpenAPI documents, JSON Schema, and versioned event schemas. It
must not contain domain entities, repositories, or handwritten shared business
logic.

Current namespaces are placeholders for the first real protocol definitions:

- `openapi/control/v1`
- `schemas/agentspec/v1`
- `events/control/v1`
- `events/execution/v1`
