import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { docTopics, referenceUrl } from "../components/site/doc-topics";
const publicDir = path.resolve("public");
const docsDir = path.join(publicDir, "docs");
const pages = ["/docs/guide.html", ...docTopics.map(({ slug }) => referenceUrl(slug))];
const documents = new Map(pages.map((url) => [url, new DOMParser().parseFromString(fs.readFileSync(path.join(publicDir, url), "utf8"), "text/html")]));

describe("published site documentation", () => {
  it("is reproducible and synchronized with source Markdown, templates and images", () => {
    expect(execFileSync(process.execPath, ["scripts/build-docs.mjs", "--check"], { encoding: "utf8" })).toContain("DOCS_CHECK=PASS pages=7");
  });
  it.each(pages)("ships readable content and valid local links for %s", (url) => {
    const doc = documents.get(url)!;
    expect(doc.querySelector("h1")?.textContent?.length).toBeGreaterThan(3);
    expect(doc.querySelector("article")?.textContent?.length).toBeGreaterThan(500);
    for (const link of doc.querySelectorAll("a[href]")) {
      const href = link.getAttribute("href")!;
      if (href.startsWith("https://")) continue;
      const target = new URL(href, `http://site.test${url}`);
      expect(target.origin).toBe("http://site.test");
      if (["/", "/docs", "/console"].includes(target.pathname)) continue;
      expect(pages).toContain(target.pathname);
      if (target.hash) expect(documents.get(target.pathname)?.getElementById(decodeURIComponent(target.hash.slice(1)))).not.toBeNull();
    }
  });
  it("embeds every tutorial illustration and describes the new public routes", () => {
    const doc = documents.get("/docs/guide.html")!;
    expect([...doc.querySelectorAll("figure img")]).toHaveLength(7);
    for (const image of doc.querySelectorAll("figure img")) expect(image.getAttribute("src")).toMatch(/^data:image\//);
    expect(doc.body.textContent).toContain("26 个页面路由");
    expect(doc.body.textContent).toContain("项目主页 `/`".replaceAll("`", ""));
    expect(doc.body.textContent).toContain("控制台入口 /console");
    expect(fs.readFileSync("Dockerfile", "utf8")).toContain("COPY --from=build /app/public ./public");
    expect(JSON.parse(fs.readFileSync(path.join(docsDir, "manifest.json"), "utf8")).pages).toHaveLength(7);
  });
});

describe("tutorial visual alignment", () => {
  it("uses the homepage navigation and marks the tutorial as current", () => {
    const doc = documents.get("/docs/guide.html")!;
    expect(doc.body.dataset.guideTheme).toBe("ivory-forest");
    const nav = doc.querySelector('nav[aria-label="主导航"]')!;
    expect([...nav.querySelectorAll("a")].map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
      ["概览", "/"], ["使用教程", "/docs/guide.html"], ["文档", "/docs"],
    ]);
    expect(nav.querySelector('[aria-current="page"]')?.textContent).toBe("使用教程");
    expect(doc.querySelector('.site-header .brand')?.getAttribute('href')).toBe('/');
    expect(doc.querySelector('.site-header .nav-cta')?.getAttribute('href')).toBe('/console');
  });
  it("matches the homepage palette and retains all fourteen chapter anchors", () => {
    const doc = documents.get("/docs/guide.html")!;
    const css = fs.readFileSync("scripts/guide.css", "utf8");
    const homeCss = fs.readFileSync("components/site/site.module.css", "utf8");
    for (const color of ["#153533", "#087f68", "#fbfcf9", "#dce6e1"]) {
      expect(css).toContain(color); expect(homeCss).toContain(color);
    }
    expect(css).not.toContain("#2f60df");
    expect(doc.querySelectorAll(".content > h2")).toHaveLength(14);
    for (let i=1; i<=14; i++) expect(doc.getElementById(`chapter-${i}`)).not.toBeNull();
    expect(doc.querySelectorAll('.sidebar [data-chapter]')).toHaveLength(14);
    expect(doc.querySelectorAll('.mobile-toc [data-chapter]')).toHaveLength(14);
  });
  it("keeps the offline guide self-contained with the same theme", () => {
    const html = fs.readFileSync("../docs/user-guide/v1/部署与使用指南.html", "utf8");
    const doc = new DOMParser().parseFromString(html, "text/html");
    expect(doc.body.dataset.guideTheme).toBe("ivory-forest");
    expect(doc.querySelector('.offline-header')).not.toBeNull();
    expect(doc.querySelector('a[href="/console"]')).toBeNull();
    expect(doc.querySelectorAll('figure img[src^="data:image/"]')).toHaveLength(7);
    expect(doc.querySelectorAll('link[rel="stylesheet"], script[src]')).toHaveLength(0);
    expect(doc.querySelector('#figure-dialog')).not.toBeNull();
  });
});
