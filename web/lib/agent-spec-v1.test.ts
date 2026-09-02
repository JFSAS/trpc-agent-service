import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import {
  createSingleLLMAgentSpec,
  isAgentSpecV1,
  validateAgentSpecShape,
  type AgentSpecV1,
} from "./agent-spec-v1";

const fixture = (group: "valid" | "invalid", name: string): unknown =>
  JSON.parse(readFileSync(resolve(process.cwd(), `../api/schemas/agentspec/v1/examples/${group}/${name}`), "utf8"));

describe("AgentSpec V1 contract", () => {
  it.each(["single-llm.json", "sequence-parallel.json", "loop.json"])(
    "accepts backend valid fixture %s without rewriting it",
    (name) => {
      const value = fixture("valid", name);
      expect(isAgentSpecV1(value)).toBe(true);
      expect(JSON.parse(JSON.stringify(value))).toEqual(value);
    },
  );

  it("creates a strict single-LLM document", () => {
    const spec = createSingleLLMAgentSpec();
    expect(isAgentSpecV1(spec)).toBe(true);
    expect(spec.root).toBe("assistant");
    expect(JSON.stringify(spec)).not.toMatch(/graph|chain|cycle|edges|editor_metadata|runtime_compatibility|model_ref/);
  });

  it("rejects backend fixtures containing editor state or a legacy model_ref", () => {
    expect(validateAgentSpecShape(fixture("invalid", "editor-state.json"))).toEqual(
      expect.arrayContaining([expect.objectContaining({ code: "AGENT_SPEC_UNKNOWN_FIELD", pointer: "/editor_state" })]),
    );
    expect(validateAgentSpecShape(fixture("invalid", "unknown-field.json"))).toEqual(
      expect.arrayContaining([expect.objectContaining({ code: "AGENT_SPEC_UNKNOWN_FIELD", pointer: "/nodes/assistant/model_ref" })]),
    );
  });

  it("uses the same duplicate codes as the Go schema validator", () => {
    const slots = createSingleLLMAgentSpec();
    slots.nodes.assistant = {
      ...(slots.nodes.assistant as Extract<AgentSpecV1["nodes"][string], { kind: "llm" }>),
      tool_slots: ["search", "search"],
    };
    slots.requirements.tools.search = { capability: "web.search" };
    expect(validateAgentSpecShape(slots)).toEqual(
      expect.arrayContaining([expect.objectContaining({ code: "AGENT_SPEC_LIMIT_EXCEEDED", pointer: "/nodes/assistant/tool_slots/1" })]),
    );

    const children = fixture("valid", "sequence-parallel.json") as AgentSpecV1;
    const main = children.nodes.main;
    if (main.kind !== "sequence") throw new Error("fixture changed");
    main.children = [main.children[0], main.children[0]];
    expect(validateAgentSpecShape(children)).toEqual(
      expect.arrayContaining([expect.objectContaining({ code: "AGENT_SPEC_DUPLICATE_CHILD", pointer: "/nodes/main/children/1" })]),
    );
  });
});
