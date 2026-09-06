// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import { GET, PATCH, POST, PUT } from "./route";

afterEach(() => vi.restoreAllMocks());

const params = { params: Promise.resolve({ path: ["v1", "tenants", "tenant/a", "runtime-profiles", "profile b", "draft"] }) };

describe("Control API same-origin proxy", () => {
  it("forwards the idempotency key, cookie, exact body and encoded route, but no arbitrary headers", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ draft_revision: 4 }));
    const body = JSON.stringify({ expected_draft_revision: 3, credentials: {} });
    const request = new NextRequest("http://console.test/api/control/v1/tenants/t/runtime-profiles/p/draft?limit=20", {
      method: "PUT", body,
      headers: { accept: "application/json", "content-type": "application/json", cookie: "session=opaque", "Idempotency-Key": "persisted-attempt-key", authorization: "not-forwarded" },
    });
    const response = await PUT(request, params);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(`${(process.env.CONTROL_API_BASE ?? "http://127.0.0.1:8080").replace(/\/$/, "")}/v1/tenants/tenant%2Fa/runtime-profiles/profile%20b/draft?limit=20`);
    expect(init).toMatchObject({ method: "PUT", cache: "no-store", redirect: "manual" });
    expect(Object.fromEntries(new Headers(init?.headers))).toEqual({ accept: "application/json", "content-type": "application/json", cookie: "session=opaque", "idempotency-key": "persisted-attempt-key" });
    expect(new TextDecoder().decode(init?.body as ArrayBuffer)).toBe(body);
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(await response.json()).toEqual({ draft_revision: 4 });
  });

  it("preserves multiple Set-Cookie headers and Retry-After on an upstream error while disabling browser caching", async () => {
    const upstream = new Response("rate limited", { status: 429, headers: { "content-type": "text/plain", "retry-after": "10", "cache-control": "public, max-age=300" } });
    upstream.headers.append("set-cookie", "session=new; HttpOnly; Path=/");
    upstream.headers.append("set-cookie", "refresh=new; HttpOnly; Path=/");
    vi.spyOn(globalThis, "fetch").mockResolvedValue(upstream);
    const response = await POST(new NextRequest("http://console.test/api/control/v1/auth/login", { method: "POST", body: "{}" }), { params: Promise.resolve({ path: ["v1", "auth", "login"] }) });
    expect(response.status).toBe(429);
    expect(response.headers.getSetCookie()).toEqual(["session=new; HttpOnly; Path=/", "refresh=new; HttpOnly; Path=/"]);
    expect(response.headers.get("retry-after")).toBe("10");
    expect(response.headers.get("content-type")).toBe("text/plain");
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(await response.text()).toBe("rate limited");
  });

  it("sends GET without a body and marks its response no-store", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ config: {} }));
    const response = await GET(new NextRequest("http://console.test/api/control/v1/runtime-profiles"), params);
    expect(fetchMock.mock.calls[0][1]?.body).toBeUndefined();
    expect(response.headers.get("cache-control")).toBe("no-store");
  });

  it("preserves the preflight 202 receipt location and retry interval", async () => {
    const path = ["v1", "tenants", "t", "channel-accounts", "cha_test", "preflights"];
    const location = "/v1/tenants/t/channel-accounts/cha_test/preflights/cpf_test";
    const receipt = { preflight_id: "cpf_test", tenant_id: "t", account_id: "cha_test", requested_at: "2026-09-06T12:00:00Z", job_deadline_at: "2026-09-06T12:02:00Z", status_url: location };
    vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(receipt, { status: 202, headers: { Location: location, "Retry-After": "2" } }));
    const response = await POST(new NextRequest(`http://console.test/api/control/${path.join("/")}`, { method: "POST", body: JSON.stringify({ expected_account_revision: 1, expected_connection_revision: 1, expected_bot_token_version: 1 }), headers: { "Idempotency-Key": "original-preflight" } }), { params: Promise.resolve({ path }) });
    expect(response.status).toBe(202);
    expect(response.headers.get("location")).toBe(location);
    expect(response.headers.get("retry-after")).toBe("2");
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(await response.json()).toEqual(receipt);
  });

  it("preserves the fallback Set-Cookie behavior when getSetCookie is unavailable", async () => {
    const upstream = Response.json({}, { headers: { "set-cookie": "session=legacy; HttpOnly; Path=/" } });
    Object.defineProperty(upstream.headers, "getSetCookie", { value: undefined });
    vi.spyOn(globalThis, "fetch").mockResolvedValue(upstream);
    const response = await GET(new NextRequest("http://console.test/api/control/v1/me"), params);
    expect(response.headers.get("set-cookie")).toBe("session=legacy; HttpOnly; Path=/");
  });

  it("returns a non-cacheable 502 on a network failure", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("connection refused"));
    const response = await GET(new NextRequest("http://console.test/api/control/v1/me"), params);
    expect(response.status).toBe(502);
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(await response.json()).toEqual({ error: { code: "CONTROL_API_UNAVAILABLE", message: "Control API is unavailable" } });
  });
  it.each([200, 201, 409, 422, 503])("preserves Deployment publication status %s, diagnostics and idempotency", async (status) => {
    const body = JSON.stringify({ expected_latest_revision_number: null, input: { schema_version: "v1", agent: { agent_id: "a", version_number: 3 }, profile: { profile_id: "p", revision_number: 2 } } });
    const payload = status < 300 ? { revision: { revision_number: 1 } } : { error: { code: "DEPLOYMENT_REVISION_INVALID" }, validation: { valid: false, diagnostics: [{ code: "DEPLOYMENT_RESOURCE_MISSING" }] } };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(payload, { status }));
    const response = await POST(new NextRequest("http://console.test/api/control/v1/tenants/t/deployments/d/revisions", { method: "POST", body, headers: { "content-type": "application/json", "Idempotency-Key": "same-publication" } }), { params: Promise.resolve({ path: ["v1", "tenants", "t", "deployments", "d", "revisions"] }) });
    expect(response.status).toBe(status); expect(await response.json()).toEqual(payload);
    expect(new Headers(fetchMock.mock.calls[0][1]?.headers).get("idempotency-key")).toBe("same-publication");
    expect(new TextDecoder().decode(fetchMock.mock.calls[0][1]?.body as ArrayBuffer)).toBe(body);
  });

  it.each([
    { method: "POST" as const, path: ["channel-accounts"], status: 201, body: { provider: "telegram", provider_account_id: "12345", name: "test account", credentials: { "telegram.bot_token": { action: "replace", value: "test-only-token" }, "telegram.webhook_secret": { action: "replace", value: "test_secret" } } } },
    { method: "PATCH" as const, path: ["channel-accounts", "account:a"], status: 200, body: { expected_account_revision: 2, name: "renamed", description: "metadata only" } },
    { method: "POST" as const, path: ["channel-accounts", "account:a", "credentials", "telegram.bot_token", "update"], status: 200, body: { expected_account_revision: 2, expected_credential_version: 1, action: "replace", value: "test-only-replacement" } },
    { method: "POST" as const, path: ["channel-accounts", "account:a", "enabled"], status: 200, body: { expected_account_revision: 2, enabled: false } },
    { method: "POST" as const, path: ["channel-bindings"], status: 201, body: { account_id: "account:a", target: { deployment_id: "deployment:a", revision_number: 3 } } },
    { method: "POST" as const, path: ["channel-bindings", "binding:a", "target"], status: 200, body: { expected_binding_revision: 4, target: { deployment_id: "deployment:a", revision_number: 3 } } },
    { method: "POST" as const, path: ["channel-bindings", "binding:a", "enabled"], status: 200, body: { expected_binding_revision: 4, enabled: true } },
  ])("forwards Channel $method $path without changing CAS, cookie, idempotency or body", async (write) => {
    const payload = { account: { id: "account:a" }, binding: null, distribution: { state: "NOT_EMITTED", event_id: null } };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(payload, { status: write.status }));
    const path = ["v1", "tenants", "tenant:a", ...write.path];
    const body = JSON.stringify(write.body);
    const request = new NextRequest(`http://console.test/api/control/${path.map(encodeURIComponent).join("/")}`, {
      method: write.method, body,
      headers: { "content-type": "application/json", accept: "application/json", cookie: "session=opaque", "Idempotency-Key": "channel-original-attempt", authorization: "not-forwarded", "x-gateway-id": "not-forwarded" },
    });
    const response = await ({ POST, PATCH }[write.method])(request, { params: Promise.resolve({ path }) });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(`${(process.env.CONTROL_API_BASE ?? "http://127.0.0.1:8080").replace(/\/$/, "")}/${path.map(encodeURIComponent).join("/")}`);
    expect(init).toMatchObject({ method: write.method, cache: "no-store", redirect: "manual" });
    expect(Object.fromEntries(new Headers(init?.headers))).toEqual({ accept: "application/json", "content-type": "application/json", cookie: "session=opaque", "idempotency-key": "channel-original-attempt" });
    expect(new TextDecoder().decode(init?.body as ArrayBuffer)).toBe(body);
    expect(response.status).toBe(write.status);
    expect(response.headers.get("cache-control")).toBe("no-store");
    expect(await response.json()).toEqual(payload);
  });

});
