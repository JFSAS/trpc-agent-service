import type { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const upstream = (process.env.CONTROL_API_BASE ?? "http://127.0.0.1:8080").replace(/\/$/, "");

async function proxy(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  const { path } = await context.params;
  const target = `${upstream}/${path.map(encodeURIComponent).join("/")}${request.nextUrl.search}`;
  const headers = new Headers();
  for (const name of ["accept", "content-type", "cookie", "idempotency-key"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  // Only the owner-defined legacy create interpreter is forwarded; it changes no authorization.
  if (request.method === "POST" && path.length === 4 && path[0] === "v1" && path[1] === "tenants" && path[3] === "channel-accounts") {
    const contract = request.headers.get("x-channel-create-contract");
    if (contract) headers.set("x-channel-create-contract", contract);
  }
  const body = ["GET", "HEAD"].includes(request.method) ? undefined : await request.arrayBuffer();
  try {
    const response = await fetch(target, { method: request.method, headers, body, cache: "no-store", redirect: "manual" });
    const outgoing = new Headers({ "cache-control": "no-store" });
    const contentType = response.headers.get("content-type");
    if (contentType) outgoing.set("content-type", contentType);
    const responseHeaders = response.headers as Headers & { getSetCookie?: () => string[] };
    const cookies = responseHeaders.getSetCookie?.() ?? [];
    if (cookies.length) cookies.forEach((cookie) => outgoing.append("set-cookie", cookie));
    else if (response.headers.get("set-cookie")) outgoing.append("set-cookie", response.headers.get("set-cookie")!);
    const resultContract = response.headers.get("x-channel-result-contract");
    if (resultContract) outgoing.set("x-channel-result-contract", resultContract);
    const retryAfter = response.headers.get("retry-after");
    if (retryAfter) outgoing.set("retry-after", retryAfter);
    // An accepted diagnostic job exposes its stable Control resource, not a browser redirect.
    const location = response.headers.get("location");
    if (response.status === 202 && location) outgoing.set("location", location);
    return new Response(response.body, { status: response.status, headers: outgoing });
  } catch {
    return Response.json({ error: { code: "CONTROL_API_UNAVAILABLE", message: "Control API is unavailable" } }, { status: 502, headers: { "cache-control": "no-store" } });
  }
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
