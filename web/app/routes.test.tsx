import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import AdminOverview from "./admin/page";
import OperatorsPage from "./admin/operators/page";
import AdminTenantsPage from "./admin/tenants/page";
import UsersPage from "./admin/users/page";
import TenantDetailPage from "./tenants/[tenantId]/page";
import MyTenantsPage from "./tenants/page";

const api = vi.hoisted(() => ({
  listUsers: vi.fn().mockResolvedValue({
    users: [{ id: "usr-1", username: "alice", display_name: "Alice", status: "ACTIVE", created_at: "2026-09-01T00:00:00Z" }],
    total: 1, offset: 0, limit: 20,
  }),
  listOperators: vi.fn().mockResolvedValue({
    operators: [{ user_id: "usr-1", username: "alice", display_name: "Alice", granted_by: "bootstrap", granted_at: "2026-09-01T00:00:00Z" }],
  }),
  listAdminTenants: vi.fn().mockResolvedValue({
    tenants: [{ id: "tenant-1", slug: "team-a", name: "Team A", status: "ACTIVE", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" }],
    total: 1, offset: 0, limit: 20,
  }),
  listMyTenants: vi.fn().mockResolvedValue({
    tenants: [{ id: "tenant-1", slug: "team-a", name: "Team A", status: "ACTIVE", role: "OWNER", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z" }],
  }),
  getTenant: vi.fn().mockResolvedValue({
    id: "tenant-1", slug: "team-a", name: "Team A", status: "ACTIVE", role: "OWNER", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z",
  }),
  listMembers: vi.fn().mockResolvedValue({
    members: [{ id: "membership-1", user_id: "usr-1", role: "OWNER", created_by: "bootstrap", created_at: "2026-09-01T00:00:00Z" }],
  }),
  createUser: vi.fn(), grantOperator: vi.fn(), revokeOperator: vi.fn(),
  createTenant: vi.fn(), addMember: vi.fn(), removeMember: vi.fn(),
}));

vi.mock("../lib/control-api", () => ({ controlApi: api, ControlApiError: class extends Error {} }));
vi.mock("next/navigation", () => ({
  useParams: () => ({ tenantId: "tenant-1" }),
  usePathname: () => "/admin",
  useRouter: () => ({ replace: vi.fn(), refresh: vi.fn() }),
}));

afterEach(() => cleanup());

describe("current Control API route pages", () => {
  it.each([
    ["overview", <AdminOverview />, "管理入口"],
    ["users", <UsersPage />, "alice"],
    ["operators", <OperatorsPage />, "OPERATOR"],
    ["admin tenants", <AdminTenantsPage />, "team-a"],
    ["my tenants", <MyTenantsPage />, "Team A"],
    ["tenant members", <TenantDetailPage />, "usr-1"],
  ])("renders %s from its API response", async (_name, page, expected) => {
    render(page);
    expect(await screen.findByText(expected)).toBeInTheDocument();
  });
});
