import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AppShell } from "./app-shell";

vi.mock("next/navigation", () => ({
  usePathname: () => "/admin/users",
  useRouter: () => ({ replace: vi.fn(), refresh: vi.fn() }),
}));

describe("dashboard-01 application shell", () => {
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
});
