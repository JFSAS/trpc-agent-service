# Control Web

`control-web` is the Next.js management console for the implemented Control API
V1 Identity, Platform Admin, Tenant membership, and Agent authoring
capabilities.
It uses the shadcn `dashboard-01` source layout with the selected A-style
white, slate, and cloud-blue visual system.

## Local development

The Control API must listen on `127.0.0.1:8080` unless overridden:

```bash
CONTROL_API_BASE=http://127.0.0.1:8080 npm run dev
```

The browser talks only to the same-origin `/api/control/*` Route Handler. The
handler forwards requests and the opaque HttpOnly session cookie to Control
API, avoiding a cross-origin authentication path.

## Implemented pages

- `/login` and `/change-password`
- `/admin`, `/admin/users`, `/admin/operators`, `/admin/tenants`
- `/tenants` and `/tenants/[tenantId]`
- `/tenants/[tenantId]/agents` and `/tenants/[tenantId]/agents/new`
- `/tenants/[tenantId]/agents/[agentId]` for the editable AgentSpec V1 canvas,
  Draft compare-and-swap save, server validation, publication, and version list
- `/tenants/[tenantId]/agents/[agentId]/versions/[versionNumber]` for the
  immutable canonical AgentSpec returned by Control API

The Agent UI works in an explicit Tenant URL context and calls the real Control
API through the same-origin proxy. Product code does not contain mock Agent or
Tenant data. The initial `{}` Draft is shown as uninitialized until the user
chooses a canvas template; that choice remains a browser working copy until the
user saves it.

Draft saving always sends the current server `expected_revision`. Validation
and publication operate on the exact saved revision. A `409
AGENT_DRAFT_REVISION_CONFLICT` keeps the local AgentSpec and requires an
explicit reload; a `422 AGENT_SPEC_INVALID` renders the backend diagnostics.
Warnings do not block publication when the backend report is `valid: true`.

Runtime Profile, Deployment, Channel, Run, Preview, Agent deletion, and Agent
archival UI remain out of scope because their corresponding Agent APIs are not
implemented.

## Verification

```bash
npm test
npm run lint
npm run build
```

## Real backend Agent E2E

The E2E script uses the browser-facing `/api/control` proxy and creates real
Tenant, Agent, Draft, and AgentVersion rows. It does not intercept HTTP or use
fixtures as product data.

Start PostgreSQL and Control API, then start the production Web server. Supply
an existing Platform Operator account to the test:

```bash
CONTROL_E2E_USERNAME='<operator>' \
CONTROL_E2E_PASSWORD='<temporary-or-current-password>' \
CONTROL_E2E_NEW_PASSWORD='<replacement-password>' \
WEB_BASE_URL='http://127.0.0.1:13000' \
npm run test:e2e:real
```

The test verifies creation with revision 1 and `{}` Spec, CAS save, server
validation, `201` publication, idempotent `200` publication, immutable version
readback, metadata update, Agent listing, and stale-revision `409` behavior.
