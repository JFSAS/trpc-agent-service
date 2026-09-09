import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import AuditPage from "./tenants/[tenantId]/audit/page";
import RunDetailPage from "./tenants/[tenantId]/runs/[runId]/page";
import RunsPage from "./tenants/[tenantId]/runs/page";

const mocks = vi.hoisted(() => ({ listRuns: vi.fn(), getRun: vi.fn(), listAuditEvents: vi.fn() }));
vi.mock("next/navigation", () => ({ useParams: () => ({ tenantId: "tenant-1", runId: "run-1" }) }));
vi.mock("../lib/control-api", () => ({ controlApi: mocks }));

beforeEach(() => {
  mocks.listRuns.mockReset().mockResolvedValue({ runs: [{ run_id: "run-1", session_id: "session-1", status: "RUNNING", stage: "EXECUTION", attempts: 1, input_tokens: 12, output_tokens: 4, total_tokens: 16, reply_status: "NOT_CREATED", accepted_at: "2026-09-09T00:00:00Z" }], offset: 0, limit: 25, total: 1 });
  mocks.getRun.mockReset().mockResolvedValue({ run_id: "run-1", session_id: "session-1", status: "FAILED", stage: "FAILED", attempts: 1, input_tokens: 12, output_tokens: 4, total_tokens: 16, reply_status: "NOT_CREATED", accepted_at: "2026-09-09T00:00:00Z", admission_id: "admission-1", attempt_log: [], timeline: [{ source: "worker", category: "ATTEMPT_ENDED", status: "FAILED", reason: "MODEL_UNAVAILABLE", occurred_at: "2026-09-09T00:01:00Z" }], coverage: ["worker.run", "worker.attempt"] });
  mocks.listAuditEvents.mockReset().mockResolvedValue({ events: [{ event_id: "deployment:1", source: "control", category: "CONFIG", action: "PUBLISH", outcome: "SUCCEEDED", actor_id: "user-1", resource_type: "deployment_revision", resource_id: "revision-1", occurred_at: "2026-09-09T00:00:00Z" }], offset: 0, limit: 25, total: 1 });
});

describe("run management pages", () => {
  it("shows current business stage and opens a Run", async () => {
    render(<RunsPage />);
    expect(await screen.findByText("run-1")).toBeInTheDocument();
    expect(screen.getByText("EXECUTION")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看过程/ })).toHaveAttribute("href", "/tenants/tenant-1/runs/run-1");
  });
  it("explains a persisted failure without claiming provider delivery", async () => {
    render(<RunDetailPage />);
    expect(await screen.findByText("MODEL_UNAVAILABLE")).toBeInTheDocument();
    expect(screen.getByText(/不等于渠道已送达/)).toBeInTheDocument();
  });
  it("shows source and actor for business audit", async () => {
    render(<AuditPage />);
    await waitFor(() => expect(screen.getByText("revision-1")).toBeInTheDocument());
    expect(screen.getByText("control")).toBeInTheDocument();
    expect(screen.getByText("user-1")).toBeInTheDocument();
  });
});
