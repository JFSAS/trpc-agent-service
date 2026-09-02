import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { createSingleLLMAgentSpec, type AgentSpecV1 } from "./agent-spec-v1";
import {
  agentEditorReducer,
  createAgentEditorState,
  parseEditorPositions,
  serializeEditorPositions,
  validateAgentSpecLocally,
} from "./agent-editor-state";

const validFixture = (name: string): AgentSpecV1 =>
  JSON.parse(readFileSync(resolve(process.cwd(), `../api/schemas/agentspec/v1/examples/valid/${name}`), "utf8")) as AgentSpecV1;

describe("Agent editor state", () => {
  it.each(["single-llm.json", "sequence-parallel.json", "loop.json"])(
    "lays out and locally validates %s",
    (name) => {
      const spec = validFixture(name);
      const state = createAgentEditorState(spec);
      expect(Object.keys(state.positions).sort()).toEqual(Object.keys(spec.nodes).sort());
      expect(validateAgentSpecLocally(spec).filter((item) => item.severity === "error")).toEqual([]);
    },
  );

  it("keeps canvas coordinates outside AgentSpec", () => {
    const initial = createAgentEditorState(createSingleLLMAgentSpec());
    const moved = agentEditorReducer(initial, { type: "node.move", nodeID: "assistant", position: { x: 333, y: 222 } });
    expect(moved.positions.assistant).toEqual({ x: 333, y: 222 });
    expect(moved.spec).toBe(initial.spec);
    expect(JSON.stringify(moved.spec)).not.toContain("positions");
    expect(parseEditorPositions(serializeEditorPositions(moved.positions), ["assistant"]).assistant).toEqual({ x: 333, y: 222 });
  });

  it("adds all V1 node kinds and preserves a structurally editable tree", () => {
    let state = createAgentEditorState(createSingleLLMAgentSpec());
    state = agentEditorReducer(state, { type: "node.add", nodeID: "steps", kind: "sequence" });
    expect(state.spec.root).toBe("steps");
    expect(state.spec.nodes.steps).toEqual(expect.objectContaining({ kind: "sequence", children: ["assistant"] }));
    state = agentEditorReducer(state, { type: "node.add", nodeID: "review", kind: "llm", parentID: "steps" });
    expect(state.spec.nodes.steps).toEqual(expect.objectContaining({ children: ["assistant", "review"] }));
    state = agentEditorReducer(state, { type: "node.add", nodeID: "parallel", kind: "parallel" });
    state = agentEditorReducer(state, { type: "node.add", nodeID: "loop", kind: "loop" });
    expect(Object.values(state.spec.nodes).map((node) => node.kind)).toEqual(
      expect.arrayContaining(["llm", "sequence", "parallel", "loop"]),
    );
  });

  it("reports a node pointer for a missing reference and slot", () => {
    const spec = validFixture("single-llm.json");
    const assistant = spec.nodes.assistant;
    if (assistant.kind !== "llm") throw new Error("fixture changed");
    assistant.model_slot = "missing";
    spec.nodes.main = { kind: "sequence", children: ["assistant", "gone"] };
    spec.root = "main";
    expect(validateAgentSpecLocally(spec)).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "AGENT_SPEC_MODEL_SLOT_NOT_FOUND", node_id: "assistant", pointer: "/nodes/assistant/model_slot" }),
      expect.objectContaining({ code: "AGENT_SPEC_NODE_REFERENCE_NOT_FOUND", node_id: "main", pointer: "/nodes/main/children/1" }),
    ]));
  });
});
