# Control Web

`control-web` is the Next.js management console for the implemented Control API
V1 Identity, Platform Admin, Tenant membership, Agent authoring, Runtime
Profile, Deployment, and Channel management capabilities.
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
- `/tenants`, `/tenants/[tenantId]`, and `/tenants/[tenantId]/members`
- `/tenants/[tenantId]/agents` and `/tenants/[tenantId]/agents/new`
- `/tenants/[tenantId]/agents/[agentId]` for the editable AgentSpec V1 canvas,
  Draft compare-and-swap save, server validation, publication, and version list
- `/tenants/[tenantId]/agents/[agentId]/versions/[versionNumber]` for the
  immutable canonical AgentSpec returned by Control API
- `/tenants/[tenantId]/runtime-profiles` for the list and create dialog
- `/tenants/[tenantId]/runtime-profiles/[profileId]` for resource and credential
  configuration, Draft save, validation, and publication
- `/tenants/[tenantId]/runtime-profiles/[profileId]/revisions/[revisionNumber]`
  for the published configuration snapshot
- `/tenants/[tenantId]/deployments` and `/tenants/[tenantId]/deployments/new`
- `/tenants/[tenantId]/deployments/[deploymentId]` for preparing and publishing
  a fixed AgentVersion + ProfileRevision combination
- `/tenants/[tenantId]/deployments/[deploymentId]/revisions/[revisionNumber]`
  for the immutable, redacted RuntimeManifest and the **接入渠道** shortcut
- `/tenants/[tenantId]/channels` for cursor-paginated ChannelAccount management
- `/tenants/[tenantId]/channels/new` for Telegram or WeCom account creation
- `/tenants/[tenantId]/channels/[accountId]` for account overview, its unique
  Binding target, credentials/settings, and connection diagnostics

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

Runtime Profile and Deployment are implemented modules, not placeholders.
Publishing a Profile or Deployment does not prove resource connectivity or
start a Run. Deployment publication does not automatically bind a channel or
switch existing traffic. Run/Preview consoles and Agent deletion/archival are
not implemented in this Web console.

## Channel management: implemented source as of 2026-09-06

Channel management is implemented in the three routes listed above. This is a
source/interaction status, not a claim that a particular container is already
serving the new build or that real Provider delivery has been accepted.

The product path uses `lib/channel-api.ts` and the existing same-origin
`/api/control/*` BFF against the real Control API. There are no runtime mock
Channel rows or fake online results. Protocol/component fixtures live under
`web/test` and test files only. The BFF forwards the session cookie and
`Idempotency-Key`, preserves query parameters, and uses `no-store`. Gateway
internal mTLS APIs and admin endpoints are not browser integration points.

The first-time workflow is deliberately four separate commands:

1. Create a **disabled** ChannelAccount with a fixed Provider/physical Bot ID
   and all required Provider credentials.
2. Choose an exact published **Deployment rN**, then create its unique
   **disabled** ChannelBinding. An account can remain disabled at this step.
3. Explicitly enable **本平台接入** for the account.
4. Explicitly enable **消息路由** for the Binding.

These operations are not an atomic transaction. Stopping account ingress
preserves the Binding's route intent; restarting can therefore use the target
stored by the server at submission time, not a target locked by the prior
confirmation. Existing-account action confirmations reread current facts,
explain the impact, and use the relevant account or Binding CAS version. `READY` is not an extra
frontend prerequisite for enabling a valid route.

- **Role boundary:** active tenant MEMBER/OWNER can read; OWNER write controls
  follow the current session/tenant access check, and the Control API remains
  authoritative. A denied write removes local write controls and secrets.
- **Read-only identity/config:** Provider, physical identity, Telegram
  `webhook_path`, and WeCom `bot_id` are not editable metadata. Account PATCH
  sends only `expected_account_revision` and `name`/`description`.
- **Fixed target:** the Deployment shortcut carries only validated
  `deployment_id`/`revision_number`. Selecting an account or loading a URL
  never writes or selects `latest`; invalid/duplicate selectors are visible.
- **Pagination:** accounts use cursor + `page_size`, not offset/total pages.
  The list does not issue a detail request per row or invent target/connection
  aggregates absent from the list API.
- **State layers:** account/route intent, event distribution, Gateway route
  application, and per-instance connection observations are separate.
  `PUBLISHED` is not a route acknowledgement; `READY` is not Worker execution
  or a successful bot reply. Current `gateway_application=UNKNOWN` remains
  explicitly unknown.
- **Unknown write outcomes:** retain the original key, versions, and body for
  an explicit retry. Credential-bearing bodies stay in page memory only;
  recovery storage contains non-secret markers. After refresh, the user must
  re-enter original values before same-key replay. A replay content conflict
  clears secret input and permits corrected original-value re-entry without
  silently generating another key. Unreadable/unwritable recovery storage
  blocks new submissions rather than discarding the recovery boundary.
- **Accepted write / failed read:** retry only the GET. A confirmed create
  opens its returned account ID, even if the following read fails; it does
  not submit another create.

Implementation map:

- `components/channels/account-list.tsx`: list, role presentation, pagination.
- `components/channels/account-create.tsx`: Provider forms and create recovery.
- `components/channels/account-workspace.tsx`: dual switches, target,
  per-purpose credentials, metadata, and layered observations.
- `components/channels/target-selector.tsx`: exact published revision choice.
- `components/channels/use-channel-access.ts` and `confirmation-dialog.tsx`:
  access state and accessible confirmation.
- `lib/channel-api.ts` and `lib/channel-editor-state.ts`: closed public DTOs,
  errors/timeouts, CAS/idempotency, and non-secret recovery markers.
- `app/tenants/[tenantId]/channels/`: async route wrappers for all three pages.

See the [ChannelBinding flow and implementation record](../docs/architecture-next/web/channelbinding-v1-flow-plan.md)
for contracts, concurrency boundaries, and the remaining acceptance matrix.
Actual container/HTTP rollout results are recorded independently from these
source checks. Telegram ingress to durable `RunRequested`, Worker execution,
and final replies are separate acceptance stages; real WeCom validation is
also independent. This README does not claim that those stages passed in the
current frontend implementation run.

## Telegram 预检 V1：实现中 / 待联调（2026-09-06）

The [cross-component contract](../docs/architecture-next/channel-preflight-v1.md)
has been frozen, but this section is **implementation in progress**, not a
claim that the current Control/Gateway/Web containers expose the new API or
that real Telegram preflight has passed. The
[central freeze record](../docs/architecture-next/channel-preflight-v1.md#10-冻结记录)
identifies Control as the sole wire owner. Its repository path is
`docs/architecture-next/control-api/telegram-preflight-v1.md` (§3–9); the
Gateway implementation design is
`docs/architecture-next/channel-gateway/telegram-preflight-v1.md`.
Both backend documents are being integrated from their own worktrees; their
links become repository links after the coordinating task pulls them. Do not
copy a second, divergent JSON contract into this README.

- **API counts:** retain the original **11 public management operations** and
  **3 internal mTLS operations**. Preflight adds **2 public operations**
  (OWNER POST to create; ACTIVE MEMBER GET to read) and **3 private operations**
  (claim, diagnostic Token resolve, complete). The combined 13/6 counts describe
  the integration target, not deployed capabilities. Preflight creation is a
  separate diagnostic task, not another account/Binding state mutation.
- **Independent prerequisite:** an existing disabled Telegram account may be
  checked without a Binding, Deployment target, `READY`, or Worker. Only
  `getMe` and `getWebhookInfo` contact Telegram. No account enable, temporary
  handler install, `setWebhook`, `deleteWebhook`, `getUpdates`, or message send
  accompanies a check.
- **Eight checks on a completed result:** credential configuration, bot
  identity, public origin, existing Webhook relationship, pending updates,
  historical delivery errors, recovery materials, and real delivery
  verification. The first six determine overall outcome; the last two remain
  visibly UNKNOWN. PASS means **配置检查通过**, never **上线成功**. Queued,
  running, timed-out, or stale jobs must not invent completed check rows.
- **Facts and freshness:** public origin validation is static, not a delivery
  probe. Old Webhook URLs are compared inside the adapter, not exposed or used
  as another HTTP destination. `freshness=CURRENT` refers only to the saved
  account connection identity and result TTL; claimed Gateway configuration
  remains `UNCONFIRMED`. Name/description-only updates can set
  `metadata_changed=true` without invalidating the connection facts. Rotation,
  credential clearing, enable/disable ABA, and TTL expiry have separate stale
  or expired states. Always show **真实消息投递未验证** and **旧 Secret 不可回读**.
- **Recovery and sharing:** the in-progress frontend stores only non-secret
  task/request markers in user + tenant + account-scoped `sessionStorage`.
  Identity changes/logout clear those markers. The same-account
  `?preflight=<id>` link lets another authorized MEMBER view the task; it does
  not grant access or trigger a check. Build the same-origin GET from validated
  IDs instead of following an arbitrary returned `status_url`.

The following adjacent UX improvements are already present in source:
[account-workspace.tsx](components/channels/account-workspace.tsx) saves
metadata in one explicit action and keeps the **edit-start account CAS** even
when background reads update the page. It presents the fixed target primarily
as **deployment name · rN**, retaining IDs as technical details and showing a
name-read error rather than changing the saved target. The
[app shell](components/app-shell.tsx) says **已登录**, not “the platform/channel is
online.” These source observations still require final authenticated acceptance.

Preflight source map — **currently being developed; the coordinating task will
mark completion only after final review and live integration**:

| File | In-progress responsibility |
|---|---|
| [channel-preflight-api.ts](lib/channel-preflight-api.ts) | Separate closed DTO guards, POST/GET, error mapping, validated share URL and non-secret storage namespace |
| [channel-preflight-api.test.ts](lib/channel-preflight-api.test.ts) | Frozen-wire and invalid-response checks |
| [channel-preflight-fixtures.ts](test/channel-preflight-fixtures.ts) | Protocol/component fixtures only, never runtime product data |
| [preflight-panel.test.tsx](components/channels/preflight-panel.test.tsx) | Panel state, permissions, result rows and recovery tests under development |
| `web/components/channels/preflight-panel.tsx` | Panel implementation in progress; integration and final source verification pending |

Real read-only Telegram checks, unchanged Webhook/account/Binding/route state,
image revisions, and browser evidence are independent release gates. Existing
Channel component tests or the historical Telegram message acceptance record
are not substitutes for the new preflight acceptance.

## Verification

Run from `web/`:

```bash
npm test -- --maxWorkers=2
npm run lint
npm run build
```

These commands cover automated source/protocol/component checks and the
production build. Passing them does not substitute for an authenticated real
backend workflow or Provider delivery acceptance. Test counts are intentionally
not frozen here; use the output attached to the relevant delivery record.

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
