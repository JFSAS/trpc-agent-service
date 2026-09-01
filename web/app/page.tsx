"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { controlApi, ControlApiError } from "../lib/control-api";

export default function Home() {
  const router = useRouter();
  useEffect(() => {
    void controlApi.getMe().then(async (session) => {
      if (session.password_change_required) return router.replace("/change-password");
      try {
        await controlApi.getCapabilities();
        router.replace("/admin");
      } catch (error) {
        if (error instanceof ControlApiError && error.status === 403) router.replace("/tenants");
        else throw error;
      }
    }).catch(() => router.replace("/login"));
  }, [router]);
  return <main className="center-page"><div className="loading-mark" /><h1>正在打开控制台</h1><p>正在恢复安全会话…</p></main>;
}
