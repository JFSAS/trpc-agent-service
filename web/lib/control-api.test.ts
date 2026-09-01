import { afterEach, describe, expect, it, vi } from "vitest";

import { controlApi } from "./control-api";

afterEach(() => vi.restoreAllMocks());

describe("Control API browser client", () => {
  it("logs in through the same-origin proxy and includes the session cookie", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({
        user: { id: "user-1", username: "admin", display_name: "Platform Admin" },
        password_change_required: false,
      }), { status: 200, headers: { "content-type": "application/json" } }),
    );

    await controlApi.login({ username: "admin", password: "secret" });

    expect(fetchMock).toHaveBeenCalledWith("/api/control/v1/auth/login", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: "admin", password: "secret" }),
    });
  });

  it("uses the implemented Admin users endpoint with pagination", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ users: [], offset: 20, limit: 20, total: 0 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );

    await controlApi.listUsers({ offset: 20, limit: 20 });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/control/v1/admin/users?offset=20&limit=20",
      { credentials: "include" },
    );
  });

  it("exposes the backend error code to the page", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({
        error: { code: "INVALID_CREDENTIALS", message: "invalid username or password" },
      }), { status: 401, headers: { "content-type": "application/json" } }),
    );

    await expect(controlApi.login({ username: "admin", password: "bad" })).rejects.toMatchObject({
      status: 401,
      code: "INVALID_CREDENTIALS",
    });
  });

  it("maps every currently implemented authenticated operation", async () => {
    const jsonResponse = (body: unknown) => new Response(JSON.stringify(body), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(jsonResponse({ user: { id: "u", username: "a", display_name: "A" }, password_change_required: false }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse({ capabilities: [] }))
      .mockResolvedValueOnce(jsonResponse({ operators: [] }))
      .mockResolvedValueOnce(jsonResponse({ user_id: "u", granted_at: "2026-09-01T00:00:00Z" }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse({ id: "u", username: "a", display_name: "A" }))
      .mockResolvedValueOnce(jsonResponse({ tenants: [], offset: 0, limit: 20, total: 0 }))
      .mockResolvedValueOnce(jsonResponse({ id: "t", slug: "team", name: "Team", status: "ACTIVE", created_at: "", updated_at: "" }))
      .mockResolvedValueOnce(jsonResponse({ tenants: [] }))
      .mockResolvedValueOnce(jsonResponse({ id: "t", slug: "team", name: "Team", status: "ACTIVE", created_at: "", updated_at: "" }))
      .mockResolvedValueOnce(jsonResponse({ members: [] }))
      .mockResolvedValueOnce(jsonResponse({ id: "m", user_id: "u", role: "MEMBER", created_by: "owner", created_at: "" }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));

    await controlApi.getMe();
    await controlApi.logout();
    await controlApi.changePassword({ current_password: "old", new_password: "new" });
    await controlApi.getCapabilities();
    await controlApi.listOperators();
    await controlApi.grantOperator("u");
    await controlApi.revokeOperator("u");
    await controlApi.createUser({ username: "a", display_name: "A", temporary_password: "secret" });
    await controlApi.listAdminTenants({ offset: 0, limit: 20 });
    await controlApi.createTenant({ slug: "team", name: "Team", owner_user_id: "u" });
    await controlApi.listMyTenants();
    await controlApi.getTenant("t");
    await controlApi.listMembers("t");
    await controlApi.addMember("t", "u");
    await controlApi.removeMember("t", "u");

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? "GET"])).toEqual([
      ["/api/control/v1/me", "GET"],
      ["/api/control/v1/auth/logout", "POST"],
      ["/api/control/v1/me/change-password", "POST"],
      ["/api/control/v1/admin/capabilities", "GET"],
      ["/api/control/v1/admin/operators", "GET"],
      ["/api/control/v1/admin/operators", "POST"],
      ["/api/control/v1/admin/operators/u", "DELETE"],
      ["/api/control/v1/admin/users", "POST"],
      ["/api/control/v1/admin/tenants?offset=0&limit=20", "GET"],
      ["/api/control/v1/admin/tenants", "POST"],
      ["/api/control/v1/me/tenants", "GET"],
      ["/api/control/v1/tenants/t", "GET"],
      ["/api/control/v1/tenants/t/members", "GET"],
      ["/api/control/v1/tenants/t/members", "POST"],
      ["/api/control/v1/tenants/t/members/u", "DELETE"],
    ]);
  });
});
