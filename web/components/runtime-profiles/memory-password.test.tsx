import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ProfileResourceEditor } from "./profile-resource-editor";
import type { ProfileConfig, CredentialActions } from "../../lib/runtime-profile-api";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
function Harness({ configured = false, readOnly = false }: { configured?: boolean; readOnly?: boolean }) {
  const [config, set] = useState<ProfileConfig>({ models: {}, tools: {}, knowledge: {}, storage: { memory: { kind: "managed_memory", backend_id: "pg", backend_revision: 1 } } });
  const [credentials, setCredentials] = useState<CredentialActions>({});
  return <><ProfileResourceEditor tenantId="t" config={config} credentials={credentials} credentialStates={configured ? { storage: { memory: { dsn_password: { configured: true, status: "active", credential_revision: 2 } } } } : {}} isOwner readOnly={readOnly} onChange={(next, actions) => { set(next); setCredentials(actions); }} /><output data-testid="payload">{JSON.stringify({ config, credentials })}</output></>;
}
const catalog = (kind: string) => vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ items: [{ id: "pg", revision: 1, label: "Memory", kind, roles: ["memory"], available: true }] }));
it("writes a raw PG password only in credential actions and preserves config", async () => {
  catalog("postgresql");render(<Harness configured />);fireEvent.click(screen.getByRole("button", { name: /^Storage ·/ }));
  const action = await screen.findByLabelText("PostgreSQL Memory 密码 操作");
  fireEvent.change(action, { target: { value: "replace" } });
  const input = screen.getByLabelText("PostgreSQL Memory 密码 新值");expect(input).toHaveValue("");expect(input).toHaveAttribute("type", "password");
  fireEvent.change(input, { target: { value: "test-raw-password" } });
  let payload = JSON.parse(screen.getByTestId("payload").textContent!);
  expect(payload.config.storage.memory).toEqual({ kind: "managed_memory", backend_id: "pg", backend_revision: 1 });
  expect(payload.credentials.storage.memory.dsn_password).toEqual({ action: "replace", value: "test-raw-password" });
  fireEvent.change(action, { target: { value: "clear" } });payload = JSON.parse(screen.getByTestId("payload").textContent!);expect(payload.credentials.storage.memory.dsn_password).toEqual({ action: "clear" });
  fireEvent.change(action, { target: { value: "keep" } });expect(JSON.parse(screen.getByTestId("payload").textContent!).credentials).toEqual({});
});
it("does not offer PG credential editing for Redis", async () => {
  catalog("redis");render(<Harness />);fireEvent.click(screen.getByRole("button", { name: /^Storage ·/ }));await screen.findByRole("option", { name: /redis/ });
  expect(screen.queryByLabelText("PostgreSQL Memory 密码 操作")).toBeNull();
});
it("preserves configured state but pauses draft editing when directory is unavailable", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 503 }));render(<Harness configured />);fireEvent.click(screen.getByRole("button", { name: /^Storage ·/ }));
  await screen.findByRole("alert");expect(screen.getByRole("region", { name: "PostgreSQL Memory 密码" })).toBeInTheDocument();
  expect(screen.queryByLabelText("PostgreSQL Memory 密码 操作")).toBeNull();expect(JSON.parse(screen.getByTestId("payload").textContent!).credentials).toEqual({});
});
it("shows immutable credential status without fetching current directory", () => {
  const fetcher = vi.spyOn(globalThis, "fetch");render(<Harness configured readOnly />);fireEvent.click(screen.getByRole("button", { name: /^Storage ·/ }));
  expect(screen.getByRole("region", { name: "PostgreSQL Memory 密码" })).toBeInTheDocument();expect(fetcher).not.toHaveBeenCalled();
});
