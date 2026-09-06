"use client";

import {
  Bot,
  Building2,
  ChevronDown,
  Hexagon,
  LayoutDashboard,
  LogOut,
  Package,
  Radio,
  ShieldCheck,
  SlidersHorizontal,
  Users,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";

import { clearDeploymentPreparations, establishPreparationIdentity } from "../lib/deployment-editor-state";
import { clearChannelPreparations, establishChannelIdentity } from "../lib/channel-editor-state";
import { clearChannelPreflightPreparations, establishChannelPreflightIdentity } from "../lib/channel-preflight-api";

import { controlApi, type Tenant, type User } from "../lib/control-api";

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
  const tenantId = tenantPath ? decodePathSegment(tenantPath) : null;
  const [resolvedTenant, setResolvedTenant] = useState<Tenant | null>(null);
  const activeTenant = resolvedTenant?.id === tenantId ? resolvedTenant : null;

  useEffect(() => {
    let cancelled = false;
    if (!tenantId) {
      setResolvedTenant(null);
      return;
    }
    void controlApi.getTenant(tenantId).then((tenant) => {
      if (!cancelled) setResolvedTenant(tenant);
    }).catch(() => {
      if (!cancelled) setResolvedTenant(null);
    });
    return () => { cancelled = true; };
  }, [tenantId]);

  useEffect(() => {
    try { establishPreparationIdentity(sessionStorage, user.id); } catch { /* Deployment surfaces show persistence failures. */ }
    try { establishChannelIdentity(sessionStorage, user.id); } catch { /* Channel surfaces show persistence failures. */ }
    try { establishChannelPreflightIdentity(sessionStorage, user.id); } catch { /* Preflight surfaces show persistence failures. */ }
  }, [user.id]);

  async function logout() {
    try { clearDeploymentPreparations(sessionStorage); } catch { /* Do not block logout if browser storage is unavailable. */ }
    try { clearChannelPreparations(sessionStorage); } catch { /* Continue logout even if browser storage is unavailable. */ }
    try { clearChannelPreflightPreparations(sessionStorage); } catch { /* Read-only diagnostic recovery must not survive logout. */ }
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
        <div className="sidebar-section-label">{isOperator ? "平台管理" : "工作区"}</div>
        <nav className="sidebar-nav" aria-label={isOperator ? "平台管理" : "租户空间"}>
          {isOperator && adminNavigation.map(({ href, label, icon: Icon }) => {
            const active = href === "/admin" ? pathname === href : pathname.startsWith(href);
            return (
              <Link className={active ? "nav-link active" : "nav-link"} href={href} key={href}>
                <Icon size={18} />
                <span>{label}</span>
              </Link>
            );
          })}
          <Link className={pathname === "/tenants" ? "nav-link active" : "nav-link"} href="/tenants">
            <Building2 size={18} />
            <span>{tenantPath ? "切换租户" : "选择租户"}</span>
          </Link>
        </nav>
        {tenantPath && (
          <>
            <div className="sidebar-section-label">租户工作区</div>
            <nav className="sidebar-nav" aria-label="租户工作区">
              <Link
                className={pathname.startsWith(`/tenants/${tenantPath}/agents`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/agents`}
              >
                <Bot size={18} />
                <span>Agent 工作台</span>
              </Link>
              <Link
                className={pathname.startsWith(`/tenants/${tenantPath}/runtime-profiles`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/runtime-profiles`}
              >
                <SlidersHorizontal size={18} />
                <span>运行配置</span>
              </Link>
              <Link
                className={pathname.startsWith(`/tenants/${tenantPath}/deployments`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/deployments`} aria-label="部署" title="部署"
              >
                <Package size={18} /><span>部署</span>
              </Link>
              <Link
                className={pathname === `/tenants/${tenantPath}/channels` || pathname.startsWith(`/tenants/${tenantPath}/channels/`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/channels`}
              >
                <Radio size={18} /><span>渠道接入</span>
              </Link>
              {activeTenant?.role === "OWNER" && <Link
                className={pathname.startsWith(`/tenants/${tenantPath}/members`) ? "nav-link active" : "nav-link"}
                href={`/tenants/${tenantPath}/members`}
              >
                <Users size={18} />
                <span>成员管理</span>
              </Link>}
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
          <div><span>{tenantPath ? activeTenant?.name ?? "租户工作区" : isOperator ? "平台管理" : "租户空间"}</span><b>/</b><strong>{pageLabel(pathname)}</strong></div>
          <div className="header-user" title="登录状态不代表渠道连接、路由应用或 Agent 执行正常"><span className="status-dot" />已登录</div>
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
  if (/^\/tenants\/[^/]+\/runtime-profiles\/[^/]+\/revisions\/[^/]+/.test(pathname)) return "运行配置版本";
  if (/^\/tenants\/[^/]+\/runtime-profiles/.test(pathname)) return "运行配置";
  if (/^\/tenants\/[^/]+\/deployments\/[^/]+\/revisions/.test(pathname)) return "部署发布版本";
  if (/^\/tenants\/[^/]+\/deployments/.test(pathname)) return "部署";
  if (/^\/tenants\/[^/]+\/channels\/new(?:\/|$)/.test(pathname)) return "新增渠道";
  if (/^\/tenants\/[^/]+\/channels\/[^/]+/.test(pathname)) return "渠道账户详情";
  if (/^\/tenants\/[^/]+\/channels(?:\/|$)/.test(pathname)) return "渠道接入";
  if (/^\/tenants\/[^/]+\/members/.test(pathname)) return "成员管理";
  if (/^\/tenants\/[^/]+/.test(pathname)) return "Agent 工作台";
  if (pathname.startsWith("/tenants")) return "选择租户";
  return "总览";
}

function decodePathSegment(segment: string) {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}
