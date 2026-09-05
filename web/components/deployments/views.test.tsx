import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DeploymentList } from "./list";
import { DeploymentHistory } from "./history";
import { DeploymentRevisionDetail } from "./revision";
import { DeploymentReturnLink } from "./return-link";
import { SourceSelector } from "./source-selector";
import { diagnosticRepair, ValidationReport } from "./validation-report";
import { ProfileResourceEditor } from "../runtime-profiles/profile-resource-editor";
import { deployment, input, invalidReport, profileRevision, revision, validReport } from "../../test/deployment-fixtures";
import { selectionFrom } from "../../lib/deployment-editor-state";
const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), getRevision: vi.fn(), listRevisions: vi.fn(), getAgent: vi.fn(), getProfile: vi.fn(), listAgents: vi.fn(), listVersions: vi.fn(), listProfiles: vi.fn(), listProfileRevisions: vi.fn() }));
vi.mock("../../lib/deployment-api", async (original) => ({ ...await original<typeof import("../../lib/deployment-api")>(), deploymentApi: { list: mocks.list, get: mocks.get, getRevision: mocks.getRevision, listRevisions: mocks.listRevisions } }));
vi.mock("../../lib/control-api", () => ({ controlApi: { getAgent: mocks.getAgent, listAgents: mocks.listAgents, listAgentVersions: mocks.listVersions } }));
vi.mock("../../lib/runtime-profile-api", async (original) => ({ ...await original<typeof import("../../lib/runtime-profile-api")>(), runtimeProfileApi: { getProfile: mocks.getProfile, listProfiles: mocks.listProfiles, listRevisions: mocks.listProfileRevisions } }));
beforeEach(() => {
  vi.resetAllMocks(); window.history.replaceState({}, "", "/");
  mocks.list.mockResolvedValue({ deployments: [deployment], total: 21 }); mocks.get.mockResolvedValue(deployment);
  mocks.getRevision.mockResolvedValue(revision); mocks.listRevisions.mockResolvedValue({ revisions: [revision], total: 21 });
  mocks.listAgents.mockResolvedValue({ agents: [{ id: "a", name: "研究流程", latest_version_number: 3 }, { id: "b", name: "另一个流程", latest_version_number: null }], total: 2 });
  mocks.listVersions.mockResolvedValue({ versions: [{ version_number: 3, published_at: "2026-09-05" }], total: 21 });
  mocks.listProfiles.mockResolvedValue({ runtime_profiles: [{ id: "p", name: "研究资源", latest_revision_number: 2 }], total: 1 }); mocks.listProfileRevisions.mockResolvedValue({ revisions: [profileRevision], total: 1 });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe("Deployment list and immutable views", () => {
  it("uses paged metadata without N+1 reads or invented runtime statuses", async () => {
    render(<DeploymentList tenantId="t" />); await screen.findByText(deployment.name);
    expect(screen.getByText("尚未发布")).toBeInTheDocument(); expect(mocks.get).not.toHaveBeenCalled(); expect(mocks.getRevision).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "下一页" })); await waitFor(() => expect(mocks.list).toHaveBeenLastCalledWith("t", 20));
    expect(screen.queryByText("运行中")).not.toBeInTheDocument();
  });
  it("keeps an empty list distinct from a load failure and allows retry", async () => {
    mocks.list.mockRejectedValueOnce(new Error("list unavailable")).mockResolvedValue({ deployments: [], total: 0 });
    render(<DeploymentList tenantId="t" />); fireEvent.click(await screen.findByRole("button", { name: "重新读取部署" }));
    await waitFor(() => expect(mocks.list).toHaveBeenCalledTimes(2)); expect(screen.queryByText("list unavailable")).not.toBeInTheDocument();
  });
  it("loads history summaries, opens exact snapshots and explicitly prepares old input", async () => {
    const prepare = vi.fn(); render(<DeploymentHistory tenantId="t" deploymentId="d" onPrepare={prepare} />);
    fireEvent.click(await screen.findByRole("button", { name: "基于 r1 准备" })); expect(prepare).toHaveBeenCalledWith(1);
    expect(screen.getByRole("link", { name: "r1" })).toHaveAttribute("href", "/tenants/t/deployments/d/revisions/1"); expect(mocks.getRevision).not.toHaveBeenCalled();
  });
  it("renders real public manifest node allocations and fixed source links", async () => {
    render(<DeploymentRevisionDetail tenantId="t" deploymentId="d" revisionNumber={1} />);
    const row = await screen.findByRole("row", { name: /assistant llm primary search docs knowledge\/docs、tools\/search/ });
    expect(within(row).getByText("search")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看 Agent v3/ })).toHaveAttribute("href", "/tenants/t/agents/a/versions/3");
    expect(screen.getByRole("link", { name: "基于此版本准备新发布" })).toHaveAttribute("href", "/tenants/t/deployments/d?from=1&prepare=1");
    expect(screen.queryByRole("button", { name: /启动|切流|停用/ })).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("credential_id");
  });
  it("rejects invalid revision numbers before fetching", async () => {
    render(<DeploymentRevisionDetail tenantId="t" deploymentId="d" revisionNumber={NaN} />);
    await screen.findByText(/版本号必须为正整数/); expect(mocks.getRevision).not.toHaveBeenCalled();
  });
});
describe("Source selection and repair navigation", () => {
  it("paginates fixed versions and resets version when its parent changes", async () => {
    const change = vi.fn(); render(<SourceSelector tenantId="t" value={selectionFrom(input)} onChange={change} disabled={false} />);
    fireEvent.click(await screen.findByRole("button", { name: "下一页Agent 固定版本" }));
    await waitFor(() => expect(mocks.listVersions).toHaveBeenLastCalledWith("t", "a", { offset: 20, limit: 20 }));
    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), { target: { value: "b" } }); expect(change).toHaveBeenCalledWith({ ...selectionFrom(input), agentId: "b", agentVersion: 0 });
    expect(screen.getByRole("option", { name: /另一个流程 · 尚未发布/ })).toBeInTheDocument();
  });
  it("resolves source names for fixed deep links outside the current list page", async () => {
    mocks.listAgents.mockResolvedValue({ agents: [], total: 21 }); mocks.listProfiles.mockResolvedValue({ runtime_profiles: [], total: 21 });
    mocks.getAgent.mockResolvedValue({ name: "跨页 Agent 名称" }); mocks.getProfile.mockResolvedValue({ name: "跨页 Profile 名称" });
    render(<SourceSelector tenantId="t" value={selectionFrom(input)} onChange={vi.fn()} disabled={false} />);
    expect(await screen.findByRole("option", { name: "跨页 Agent 名称 · 当前固定选择" })).toBeInTheDocument();
    expect(await screen.findByRole("option", { name: "跨页 Profile 名称 · 当前固定选择" })).toBeInTheDocument();
  });
  it("keeps missing-resource repairs in Profile even when source is agent", () => {
    const link = diagnosticRepair("t", selectionFrom(input), invalidReport.diagnostics[0], "/tenants/t/deployments/d");
    expect(link?.href).toContain("/runtime-profiles/p?returnTo="); expect(link?.href).toContain("focusCategory=tools&focusName=search");
    expect(diagnosticRepair("t", selectionFrom(input), { ...invalidReport.diagnostics[0], source: "platform" }, "/")).toBeNull();
    expect(diagnosticRepair("t", selectionFrom(input), { ...invalidReport.diagnostics[0], code: "DEPLOYMENT_CREDENTIAL_UNAVAILABLE", source: "profile", category: null, name: null, path: "/credentials" }, "/tenants/t/deployments/d")?.href).toContain("/runtime-profiles/p/revisions/2?");
  });
  it("presents input diagnostics as a selection repair, not a platform policy", () => {
    render(<ValidationReport tenantId="t" selection={selectionFrom(input)} returnTo="/tenants/t/deployments/d" report={{ ...validReport, valid: false, diagnostics: [{ ...invalidReport.diagnostics[0], source: "input" }] }} />);
    expect(screen.getByText("请返回上方重新选择固定来源。")).toBeInTheDocument();
  });
  it("preserves same-tenant return selection and focuses a named resource", async () => {
    const onFocus = vi.fn(); window.history.replaceState({}, "", "/?" + new URLSearchParams({ returnTo: "/tenants/t/deployments/d?prepare=1", focusCategory: "tools", focusName: "search" }));
    render(<DeploymentReturnLink tenantId="t" onFocus={onFocus} />);
    expect(await screen.findByRole("link", { name: /返回部署准备/ })).toHaveAttribute("href", "/tenants/t/deployments/d?prepare=1"); expect(onFocus).toHaveBeenCalledWith({ category: "tools", name: "search" });
  });
  it("does not render a cross-tenant return or apply its focus target", () => {
    const onFocus = vi.fn(); window.history.replaceState({}, "", "/?" + new URLSearchParams({ returnTo: "/tenants/other/deployments/d", focusCategory: "tools", focusName: "search" }));
    render(<DeploymentReturnLink tenantId="t" onFocus={onFocus} />); expect(screen.queryByRole("link")).not.toBeInTheDocument(); expect(onFocus).not.toHaveBeenCalled();
  });
  it("prefills and focuses a missing Profile resource without auto-creating it", async () => {
    const change = vi.fn(); render(<ProfileResourceEditor config={profileRevision.config} credentials={{}} credentialStates={{}} onChange={change} isOwner focusTarget={{ category: "tools", name: "new_search" }} />);
    const name = screen.getByRole("textbox", { name: "新资源名称" }); await waitFor(() => expect(name).toHaveValue("new_search")); expect(name).toHaveFocus(); expect(change).not.toHaveBeenCalled();
  });
});
