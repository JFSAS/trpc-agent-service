"use client";

import {
  Bot,
  Building2,
  ChevronDown,
  Hexagon,
  LayoutDashboard,
  LogOut,
  ShieldCheck,
  Users,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { ReactNode } from "react";

import { controlApi, type User } from "../lib/control-api";

type AppShellProps = {
  user: User;
  capabilities?: string[];
  children: ReactNode;
};

const adminNavigation = [
  { href: "/admin", label: "总览", icon: LayoutDashboard },
  { href: "/admin/users", label: "平台用户", icon: Users },
  { href: "/admin/operators", label: "平台管理员", icon: ShieldCheck },
  { href: "/admin/tenants", label: "租户", icon: Building2 },
];

export function AppShell({ user, capabilities = [], children }: AppShellProps) {
  const pathname = usePathname();
  const router = useRouter();
  const isOperator = capabilities.length > 0;
  const tenantPath = pathname.match(/^\/tenants\/([^/]+)/)?.[1];

  async function logout() {
    await controlApi.logout().catch(() => undefined);
    router.replace("/login");
    router.refresh();
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <Link className="brand" href={isOperator ? "/admin" : "/tenants"}>
          <span className="brand-mark"><Hexagon size={18} strokeWidth={2.4} /></span>
          <span>Agent tRPC</span>
        </Link>
        <div className="sidebar-section-label">平台管理</div>
        <nav className="sidebar-nav" aria-label="平台管理">
          {isOperator && adminNavigation.map(({ href, label, icon: Icon }) => {
            const active = href === "/admin" ? pathname === href : pathname.startsWith(href);
            return (
              <Link className={active ? "nav-link active" : "nav-link"} href={href} key={href}>
                <Icon size={18} />
                <span>{label}</span>
              </Link>
            );
          })}
          <Link className={pathname.startsWith("/tenants") ? "nav-link active" : "nav-link"} href="/tenants">
            <Building2 size={18} />
            <span>我的租户</span>
          </Link>
        </nav>
        {tenantPath && (
          <>
            <div className="sidebar-section-label">租户工作区</div>
            <nav className="sidebar-nav" aria-label="租户工作区">
              <Link
                className={pathname === `/tenants/${tenantPath}` ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}`}
              >
                <Users size={18} />
                <span>成员</span>
              </Link>
              <Link
                className={pathname.startsWith(`/tenants/${tenantPath}/agents`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/agents`}
              >
                <Bot size={18} />
                <span>Agent 画布</span>
              </Link>
            </nav>
          </>
        )}
        <div className="sidebar-footer">
          <div className="profile-card">
            <span className="avatar">{(user.display_name || user.username).slice(0, 2).toUpperCase()}</span>
            <span className="profile-copy">
              <strong>{user.display_name}</strong>
              <small>{user.username}</small>
            </span>
            <ChevronDown size={15} />
          </div>
          <button className="logout-button" onClick={() => void logout()} type="button">
            <LogOut size={16} />退出登录
          </button>
        </div>
      </aside>
      <main className="workspace">
        <header className="workspace-header">
          <div><span>平台管理</span><b>/</b><strong>{pageLabel(pathname)}</strong></div>
          <div className="header-user"><span className="status-dot" />服务正常</div>
        </header>
        <div className="workspace-content">{children}</div>
      </main>
    </div>
  );
}

function pageLabel(pathname: string) {
  if (pathname.startsWith("/admin/users")) return "平台用户";
  if (pathname.startsWith("/admin/operators")) return "平台管理员";
  if (pathname.startsWith("/admin/tenants")) return "租户";
  if (/^\/tenants\/[^/]+\/agents\/[^/]+\/versions\/[^/]+/.test(pathname)) return "不可变版本";
  if (/^\/tenants\/[^/]+\/agents\/new/.test(pathname)) return "创建 Agent";
  if (/^\/tenants\/[^/]+\/agents\/[^/]+/.test(pathname)) return "Agent 工作台";
  if (/^\/tenants\/[^/]+\/agents/.test(pathname)) return "Agents";
  if (/^\/tenants\/[^/]+/.test(pathname)) return "租户成员";
  if (pathname.startsWith("/tenants")) return "我的租户";
  return "总览";
}
