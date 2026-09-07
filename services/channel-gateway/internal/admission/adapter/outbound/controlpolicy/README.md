# Control immutable-policy reader

This adapter fetches exact published AccessPolicy documents for Admission policy
projection construction. It is not an authorization decision or freshness source.
It is not yet attached to a broker consumer or the Admission hot path.

- Constructor pins trusted scope/epoch, HTTPS origin, private CA and client cert.
  TLS 1.3, no ambient proxy or redirects, 5-second total deadline, connection/header
  limits and bounded uncompressed JSON protect the transport boundary.
- `Fetch` accepts a validated published notification from the future trusted broker
  adapter. Scope/epoch must match local configuration before network I/O; an audit
  event is not a publication trigger. The event itself is not proof of broker trust.
- The closed request sends only account and exact policy ID/revision/digest.
  The response must match scope, epoch, tenant, account, provider and reference.
- Shared wire decoding rechecks closed schemas, duplicate/unknown/null fields,
  sorted unique sets, opaque conversation byte/character constraints, timestamp
  representation and the complete immutable envelope JCS/SHA256 digest.
- Errors return no document. Provider response bodies and TLS/URL diagnostics do
  not escape through error strings. Cancellation remains distinguishable.
- No Control internal packages or Connection domain types are imported.

A successful fetch must not refresh authorization freshness, grant an external
principal access, create a Run, or override a revoked subject. Broker consumer,
complete snapshots/watermarks, gap recovery and transactional PG projection fences
remain to be integrated. The same connection must not perform per-message synchronous
Control reads. Policy reference integrity and current authorization are separate.

Tests use real loopback mTLS plus adversarial payloads. Separate tests exercise real
Control owner preparation and real PostgreSQL responses against the shared decoder;
these are not one real broker-to-Admission production chain.
