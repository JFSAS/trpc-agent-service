import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppShell } from "./app-shell";

const navigation = vi.hoisted(() => ({ pathname: "/admin/users" }));

vi.mock("next/navigation", () => ({
  usePathname: () => navigation.pathname,
  useRouter: () => ({ replace: vi.fn(), refresh: vi.fn() }),
}));

afterEach(() => cleanup());

describe("dashboard-01 application shell", () => {
  beforeEach(() => { navigation.pathname = "/admin/users"; });

  it("shows only navigation backed by the currently implemented APIs", () => {
    render(
      <AppShell
        user={{ id: "user-1", username: "admin", display_name: "Platform Admin" }}
        capabilities={["users:manage", "operators:manage", "tenants:manage"]}
      >
        <div>page content</div>
      </AppShell>,
    );

    expect(screen.getByRole("link", { name: "平台用户" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "平台管理员" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "租户" })).toBeInTheDocument();
    expect(screen.queryByText("Agent 应用")).not.toBeInTheDocument();
    expect(screen.queryByText("部署")).not.toBeInTheDocument();
  });

  it("shows tenant-scoped Agent authoring only inside a tenant workspace", () => {
    navigation.pathname = "/tenants/tenant-1/agents/agent-1";
    render(
      <AppShell user={{ id: "user-1", username: "member", display_name: "Tenant Member" }}>
        <div>agent content</div>
      </AppShell>,
    );

    expect(screen.getByRole("link", { name: "Agent 画布" })).toHaveAttribute(
      "href",
      "/tenants/tenant-1/agents",
    );
    expect(screen.getByRole("link", { name: "成员" })).toHaveAttribute(
      "href",
      "/tenants/tenant-1",
    );
    expect(screen.queryByRole("link", { name: "平台用户" })).not.toBeInTheDocument();
  });
});
