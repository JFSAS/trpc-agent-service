import { ArrowRight, Hexagon } from "lucide-react";
import type { ReactNode } from "react";
import styles from "./site.module.css";

export const guideUrl = "/docs/guide.html";
export function SiteShell({ children, active }: { children: ReactNode; active?: "home" | "docs" }) {
  return <div className={styles.site}>
    <a className={styles.skipLink} href="#main-content">跳到主要内容</a>
    <header className={styles.header}>
      <a className={styles.brand} href="/" aria-label="Agent tRPC 主页"><span className={styles.brandIcon}><Hexagon size={23} strokeWidth={2.5} /></span>Agent <strong>tRPC</strong><span className={styles.version}>V1</span></a>
      <nav className={styles.nav} aria-label="主导航"><a href="/" aria-current={active === "home" ? "page" : undefined}>概览</a><a href={guideUrl}>使用教程</a><a href="/docs" aria-current={active === "docs" ? "page" : undefined}>文档</a></nav>
      <a className={styles.navCta} href="/console">进入控制台 <ArrowRight size={15} /></a>
    </header>{children}
    <footer className={styles.footer}><div><a className={styles.brand} href="/"><Hexagon size={21} />Agent <strong>tRPC</strong></a><p>从一个想法，到一次真实的对话。</p></div><nav aria-label="页脚导航"><a href={guideUrl}>使用教程</a><a href="/docs">文档中心</a><a href="/console">控制台</a></nav><span>BUILD WITH INTENT. SHIP WITH CONFIDENCE.</span></footer>
  </div>;
}
