# Control API migrations

Control API owns the migrations in this directory. The first migration will be
`0001_baseline.sql`, created after the Control domain model and tenant isolation
strategy are accepted.

An empty SQL migration is deliberately not included: a successful no-op
baseline would falsely imply that a production schema exists.
