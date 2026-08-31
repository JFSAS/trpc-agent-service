# Production services

Each directory below is one independently built production workload. Service
implementations are private to their own `internal/` tree; only versioned
protocols under `/api` and generated protocol code under `/gen` may be shared.

The first workload under construction is [`control-api`](control-api/README.md).
Other workloads will be added only when their architecture is ready to be
implemented; this repository does not keep empty service placeholders.
