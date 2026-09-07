"use client";

import { Hexagon } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { FormEvent, Suspense, useState } from "react";
import { Button, Field } from "../../components/ui";
import { controlApi, ControlApiError } from "../../lib/control-api";

export default function LoginPage() {
  return <Suspense><LoginForm /></Suspense>;
}

function LoginForm() {
  const router = useRouter();
  const search = useSearchParams();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault(); setError(""); setSubmitting(true);
    try {
      const session = await controlApi.login({ username, password });
      if (session.password_change_required) router.replace("/change-password");
      else {
        const requested = search.get("next");
        router.replace(requested?.startsWith("/") && !requested.startsWith("//") ? requested : "/console");
      }
    } catch (caught) {
      setError(caught instanceof ControlApiError && caught.code === "INVALID_CREDENTIALS" ? "用户名或密码错误" : caught instanceof Error ? caught.message : "登录失败");
    } finally { setSubmitting(false); }
  }
  return <main className="auth-page"><form className="auth-card" onSubmit={(event) => void submit(event)}><div className="auth-brand"><span className="brand-mark"><Hexagon size={18} /></span>Agent tRPC</div><h1>登录控制台</h1><p>使用平台账号登录本地 Control API。</p>{error && <div className="api-notice" role="alert">{error}</div>}<div className="form-stack"><Field autoComplete="username" label="用户名" onChange={(e) => setUsername(e.target.value)} placeholder="请输入用户名" required value={username} /><Field autoComplete="current-password" label="密码" onChange={(e) => setPassword(e.target.value)} placeholder="请输入密码" required type="password" value={password} /><Button disabled={submitting} type="submit">{submitting ? "正在登录…" : "登录"}</Button></div><div className="auth-foot">Session 由 HttpOnly Cookie 安全保存</div></form></main>;
}
