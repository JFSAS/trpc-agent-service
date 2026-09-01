# Control Web

`control-web` is the Next.js management console for the currently implemented
Control API V1 Identity, Platform Admin, and Tenant membership capabilities.
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

No Agent, Runtime Profile, Deployment, Channel, Run, or Audit UI is exposed
until the corresponding Control API exists.

## Verification

```bash
npm test
npm run lint
npm run build
```
