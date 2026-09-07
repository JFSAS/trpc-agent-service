import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Home from "./page";
import DocsPage from "./docs/page";
import { docTopics, referenceUrl } from "../components/site/doc-topics";
const api = vi.hoisted(() => ({ getMe: vi.fn(), getCapabilities: vi.fn() }));
vi.mock("../lib/control-api", () => ({ controlApi: api }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("public project site", () => {
  it("renders the homepage without restoring a Control API session", () => {
    render(<Home />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("让你的 Agent，真正开始工作。");
    expect(screen.queryByText("正在打开控制台")).not.toBeInTheDocument();
    expect(api.getMe).not.toHaveBeenCalled();
    expect(api.getCapabilities).not.toHaveBeenCalled();
    expect(screen.getByText("流程示意 · 非运行状态")).toBeInTheDocument();
    expect(screen.getByText(/工具、知识与组合节点的配置入口不代表/)).toBeInTheDocument();
  });
  it("exposes tutorial, documentation and the separate console entry", () => {
    render(<Home />);
    expect(screen.getByRole("link", { name: "开始搭建我的 Agent" })).toHaveAttribute("href", "/docs/guide.html#chapter-4");
    expect(screen.getByRole("link", { name: "阅读文档" })).toHaveAttribute("href", "/docs");
    for (const link of screen.getAllByRole("link", { name: "进入控制台" })) expect(link).toHaveAttribute("href", "/console");
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    expect(nav.getByRole("link", { name: "概览" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "跳到主要内容" })).toHaveAttribute("href", "#main-content");
    for (const link of screen.getAllByRole("link")) expect(link.getAttribute("href")).not.toBe("#");
  });
  it("offers six readable reference topics and three task-based paths", () => {
    render(<DocsPage />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("少一点摸索，多一点开始。");
    for (const topic of docTopics) {
      expect(screen.getByRole("heading", { name: topic.title }).closest("a")).toHaveAttribute("href", referenceUrl(topic.slug));
    }
    expect(screen.getByRole("heading", { name: "解决使用中的问题" }).closest("a")).toHaveAttribute("href", "/docs/guide.html#chapter-11");
    expect(screen.getByRole("heading", { name: "安装自己的平台" }).closest("a")).toHaveAttribute("href", "/docs/guide.html#chapter-13");
    expect(api.getMe).not.toHaveBeenCalled();
  });
});
