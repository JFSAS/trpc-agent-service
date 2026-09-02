"use client";

import { Braces, Boxes, Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";

import type { AgentSpecDocument, ValidationDiagnostic } from "../../lib/control-api";
import {
  AGENT_NODE_KINDS,
  createSingleLLMAgentSpec,
  isAgentSpecV1,
  isEmptyAgentSpec,
  type AgentNodeKind,
  type AgentNodeV1,
  type AgentSpecDiagnostic,
} from "../../lib/agent-spec-v1";
import {
  agentEditorReducer,
  createAgentEditorState,
  mergeAgentSpecDiagnostics,
  parseEditorPositions,
  serializeEditorPositions,
  validateAgentSpecLocally,
  type AgentEditorAction,
  type AgentEditorState,
  type RequirementKind,
} from "../../lib/agent-editor-state";
import { Button } from "../ui";
import { AgentCanvas } from "./agent-canvas";
import { DiagnosticsPanel } from "./diagnostics-panel";

export function AgentSpecEditor({
  value,
  onChange,
  diagnostics = [],
  disabled = false,
  storageKey,
}: {
  value: AgentSpecDocument;
  onChange(next: AgentSpecDocument): void;
  diagnostics?: readonly ValidationDiagnostic[];
  disabled?: boolean;
  storageKey?: string;
}) {
  const valueKey = useMemo(() => JSON.stringify(value), [value]);
  const [mode, setMode] = useState<"canvas" | "json">(
    isAgentSpecV1(value) || isEmptyAgentSpec(value) ? "canvas" : "json",
  );
  const [state, setState] = useState<AgentEditorState | null>(() =>
    isAgentSpecV1(value) ? createAgentEditorState(value) : null,
  );
  const [jsonSource, setJSONSource] = useState(() => JSON.stringify(value, null, 2));
  const [jsonError, setJSONError] = useState("");

  useEffect(() => {
    setJSONSource(JSON.stringify(value, null, 2));
    setJSONError("");
    if (isAgentSpecV1(value)) {
      setState((current) => current
        ? agentEditorReducer(current, { type: "spec.replace", spec: value })
        : createAgentEditorState(value));
    } else {
      setState(null);
      if (!isEmptyAgentSpec(value)) setMode("json");
    }
  }, [valueKey]);

  useEffect(() => {
    if (!state || !storageKey) return;
    const stored = window.localStorage.getItem(storageKey);
    if (!stored) return;
    const positions = parseEditorPositions(stored, Object.keys(state.spec.nodes));
    if (Object.keys(positions).length > 0) {
      setState((current) => current
        ? { ...current, positions: { ...current.positions, ...positions } }
        : current);
    }
  }, [storageKey]);

  const positionsJSON = state ? serializeEditorPositions(state.positions) : "";
  useEffect(() => {
    if (storageKey && positionsJSON) window.localStorage.setItem(storageKey, positionsJSON);
  }, [positionsJSON, storageKey]);

  const allDiagnostics = useMemo(
    () => mergeAgentSpecDiagnostics(
      validateAgentSpecLocally(value),
      diagnostics as readonly AgentSpecDiagnostic[],
    ),
    [diagnostics, valueKey],
  );

  const dispatch = useCallback((action: AgentEditorAction) => {
    if (!state || disabled) return;
    const next = agentEditorReducer(state, action);
    setState(next);
    if (next.spec !== state.spec) {
      setJSONSource(JSON.stringify(next.spec, null, 2));
      onChange(next.spec);
    }
  }, [disabled, onChange, state]);

  function initializeCanvas() {
    const spec = createSingleLLMAgentSpec();
    setState(createAgentEditorState(spec));
    setJSONSource(JSON.stringify(spec, null, 2));
    setJSONError("");
    setMode("canvas");
    onChange(spec);
  }

  function applyJSON() {
    try {
      const parsed: unknown = JSON.parse(jsonSource);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error("AgentSpec 顶层必须是 JSON Object");
      }
      const sensitiveField = findSensitiveAgentSpecField(parsed);
      if (sensitiveField) {
        throw new Error(`敏感字段 ${sensitiveField} 不能写入 Draft`);
      }
      setJSONError("");
      onChange(parsed as Record<string, unknown>);
      if (isAgentSpecV1(parsed)) {
        setState((current) => current
          ? agentEditorReducer(current, { type: "spec.replace", spec: parsed })
          : createAgentEditorState(parsed));
        setMode("canvas");
      } else {
        setState(null);
      }
    } catch (error) {
      setJSONError(error instanceof Error ? error.message : "JSON 无法解析");
    }
  }

  if (isEmptyAgentSpec(value) && !state) {
    return (
      <section aria-label="初始化 AgentSpec 画布" style={emptyStyle}>
        <Boxes color="#0f6fec" size={30} />
        <h2 style={{ fontSize: 18, margin: 0 }}>Draft 尚未初始化</h2>
        <p style={{ color: "#667085", fontSize: 12, lineHeight: 1.65, margin: 0, maxWidth: 560 }}>
          当前是后端创建的 revision 1、spec={"{}"}。模板只生成浏览器工作副本；点击页面上方“保存 Draft”后才写入 Control API。
        </p>
        <Button disabled={disabled} onClick={initializeCanvas}><Plus size={14} />使用 Single LLM 模板初始化画布</Button>
        <Button disabled={disabled} onClick={() => setMode(mode === "json" ? "canvas" : "json")} variant="ghost"><Braces size={13} />直接编辑 JSON</Button>
        {mode === "json" && <JSONEditor disabled={disabled} error={jsonError} onApply={applyJSON} onChange={setJSONSource} source={jsonSource} />}
      </section>
    );
  }

  return (
    <section style={shellStyle}>
      <header style={headerStyle}>
        <div><strong style={{ display: "block", fontSize: 14 }}>AgentSpec V1 编辑器</strong><small style={{ color: "#778292" }}>画布生成 Spec；后端负责最终校验、规范化和发布。</small></div>
        <div style={{ display: "flex", gap: 6 }}>
          <Button disabled={!state} onClick={() => setMode("canvas")} variant={mode === "canvas" ? "primary" : "secondary"}><Boxes size={13} />画布</Button>
          <Button onClick={() => { setJSONSource(JSON.stringify(value, null, 2)); setMode("json"); }} variant={mode === "json" ? "primary" : "secondary"}><Braces size={13} />JSON</Button>
        </div>
      </header>
      {mode === "json" || !state ? (
        <div style={{ display: "grid", gap: 12, padding: 14 }}>
          <JSONEditor disabled={disabled} error={jsonError} onApply={applyJSON} onChange={setJSONSource} source={jsonSource} />
          <DiagnosticsPanel diagnostics={allDiagnostics} />
        </div>
      ) : (
        <div className="agent-spec-editor-grid" style={visualGridStyle}>
          <div style={{ display: "grid", gap: 12, minWidth: 0 }}>
            <NodeToolbar disabled={disabled} dispatch={dispatch} state={state} />
            <AgentCanvas disabled={disabled} diagnostics={allDiagnostics} onAction={dispatch} state={state} />
            <DiagnosticsPanel diagnostics={allDiagnostics} onSelectNode={(nodeID) => dispatch({ type: "node.select", nodeID })} />
          </div>
          <aside style={{ alignContent: "start", display: "grid", gap: 12, minWidth: 0 }}>
            <NodeInspector disabled={disabled} dispatch={dispatch} state={state} />
            <RequirementsEditor disabled={disabled} dispatch={dispatch} state={state} />
          </aside>
        </div>
      )}
    </section>
  );
}

function JSONEditor({ source, onChange, onApply, error, disabled }: {
  source: string;
  onChange(value: string): void;
  onApply(): void;
  error: string;
  disabled: boolean;
}) {
  return (
    <div style={{ display: "grid", gap: 9, width: "100%" }}>
      <textarea aria-label="AgentSpec JSON" disabled={disabled} onChange={(event) => onChange(event.target.value)} spellCheck={false} style={jsonStyle} value={source} />
      {error && <div role="alert" style={{ color: "#b42338", fontSize: 11 }}>{error}</div>}
      <div style={{ alignItems: "center", display: "flex", gap: 10, justifyContent: "space-between" }}>
        <small style={{ color: "#778292" }}>不完整但可解析的 Object 仍可保存为 Draft。</small>
        <Button disabled={disabled} onClick={onApply}>应用 JSON 到工作副本</Button>
      </div>
    </div>
  );
}

function NodeToolbar({ state, dispatch, disabled }: {
  state: AgentEditorState;
  dispatch(action: AgentEditorAction): void;
  disabled: boolean;
}) {
  const [nodeID, setNodeID] = useState("");
  const [kind, setKind] = useState<AgentNodeKind>("llm");
  const selected = state.selectedNodeID ? state.spec.nodes[state.selectedNodeID] : undefined;
  const canOwnChildren = selected?.kind === "sequence" || selected?.kind === "parallel";
  const validID = /^[a-z][a-z0-9_-]{0,63}$/.test(nodeID) && !state.spec.nodes[nodeID];
  return (
    <div style={toolbarStyle}>
      <input aria-label="新节点 ID" disabled={disabled} onChange={(event) => setNodeID(event.target.value)} placeholder="节点 ID，例如 review" style={inputStyle} value={nodeID} />
      <select aria-label="新节点类型" disabled={disabled} onChange={(event) => setKind(event.target.value as AgentNodeKind)} style={inputStyle} value={kind}>{AGENT_NODE_KINDS.map((item) => <option key={item}>{item}</option>)}</select>
      <Button disabled={disabled || !validID} onClick={() => { dispatch({ type: "node.add", nodeID, kind, parentID: canOwnChildren ? state.selectedNodeID : null }); setNodeID(""); }}><Plus size={13} />添加节点</Button>
      <small style={{ color: "#778292", flex: 1, minWidth: 180 }}>{canOwnChildren ? `LLM 会附加到 ${state.selectedNodeID}；组合节点会包装 Root。` : "组合节点会包装当前 Root。"}</small>
    </div>
  );
}

function NodeInspector({ state, dispatch, disabled }: {
  state: AgentEditorState;
  dispatch(action: AgentEditorAction): void;
  disabled: boolean;
}) {
  const nodeID = state.selectedNodeID;
  const node = nodeID ? state.spec.nodes[nodeID] : undefined;
  if (!nodeID || !node) return <section style={panelStyle}>请选择画布节点。</section>;
  const replace = (next: AgentNodeV1) => dispatch({ type: "node.replace", nodeID, node: next });
  const candidates = Object.keys(state.spec.nodes).filter((item) => item !== nodeID);
  return (
    <section aria-label="节点属性" style={panelStyle}>
      <header style={panelHeaderStyle}>
        <div><strong>{nodeID}</strong><small style={{ color: "#778292", display: "block", marginTop: 3 }}>{node.kind}</small></div>
        <div style={{ display: "flex", gap: 5 }}>{state.spec.root !== nodeID && <Button disabled={disabled} onClick={() => dispatch({ type: "root.set", nodeID })} variant="ghost">设为 Root</Button>}<Button aria-label={`删除节点 ${nodeID}`} disabled={disabled || Object.keys(state.spec.nodes).length <= 1} onClick={() => dispatch({ type: "node.delete", nodeID })} variant="danger"><Trash2 size={13} /></Button></div>
      </header>
      <label style={fieldStyle}><span>显示名称</span><input disabled={disabled} onChange={(event) => replace({ ...node, name: event.target.value || undefined } as AgentNodeV1)} value={node.name ?? ""} /></label>
      {node.kind === "llm" && <LLMFields disabled={disabled} node={node} replace={replace} state={state} />}
      {(node.kind === "sequence" || node.kind === "parallel") && <label style={fieldStyle}><span>Children（逗号分隔、有序）</span><input disabled={disabled} onChange={(event) => replace({ ...node, children: csv(event.target.value) })} value={node.children.join(", ")} /><small>引用已有节点 ID；顺序即执行顺序。</small></label>}
      {node.kind === "loop" && <><label style={fieldStyle}><span>Body</span><select disabled={disabled} onChange={(event) => dispatch({ type: "loop.body.set", nodeID, body: event.target.value })} value={node.body}>{candidates.map((item) => <option key={item}>{item}</option>)}</select></label><label style={fieldStyle}><span>最大迭代次数</span><input disabled={disabled} min={1} max={32} onChange={(event) => replace({ ...node, max_iterations: Number(event.target.value) })} type="number" value={node.max_iterations} /></label></>}
    </section>
  );
}

function LLMFields({ node, state, replace, disabled }: {
  node: Extract<AgentNodeV1, { kind: "llm" }>;
  state: AgentEditorState;
  replace(next: AgentNodeV1): void;
  disabled: boolean;
}) {
  return <>
    <label style={fieldStyle}><span>Instruction</span><textarea disabled={disabled} onChange={(event) => replace({ ...node, instruction: event.target.value })} rows={5} value={node.instruction} /></label>
    <label style={fieldStyle}><span>Model Slot</span><select disabled={disabled} onChange={(event) => replace({ ...node, model_slot: event.target.value })} value={node.model_slot}>{Object.keys(state.spec.requirements.models).map((slot) => <option key={slot}>{slot}</option>)}</select></label>
    <label style={fieldStyle}><span>Tool Slots（逗号分隔）</span><input disabled={disabled} onChange={(event) => replace({ ...node, tool_slots: csv(event.target.value) })} value={node.tool_slots.join(", ")} /></label>
    <label style={fieldStyle}><span>Knowledge Slots（逗号分隔）</span><input disabled={disabled} onChange={(event) => replace({ ...node, knowledge_slots: csv(event.target.value) })} value={node.knowledge_slots.join(", ")} /></label>
    <div style={{ display: "grid", gap: 7, gridTemplateColumns: "1fr 1fr" }}>
      <label style={fieldStyle}><span>Temperature</span><input disabled={disabled} max={2} min={0} onChange={(event) => replace({ ...node, generation: generation(event.target.value, node.generation?.max_output_tokens) })} step={0.1} type="number" value={node.generation?.temperature ?? ""} /></label>
      <label style={fieldStyle}><span>Max Tokens</span><input disabled={disabled} max={262144} min={1} onChange={(event) => replace({ ...node, generation: generation(node.generation?.temperature, event.target.value) })} type="number" value={node.generation?.max_output_tokens ?? ""} /></label>
    </div>
  </>;
}

function RequirementsEditor({ state, dispatch, disabled }: {
  state: AgentEditorState;
  dispatch(action: AgentEditorAction): void;
  disabled: boolean;
}) {
  return <section aria-label="资源需求槽位" style={panelStyle}><header style={panelHeaderStyle}><div><strong>Requirements</strong><small style={{ color: "#778292", display: "block", marginTop: 3 }}>只声明逻辑槽位，不填写密钥</small></div></header><RequirementGroup disabled={disabled} dispatch={dispatch} entries={state.spec.requirements.models} kind="models" /><RequirementGroup disabled={disabled} dispatch={dispatch} entries={state.spec.requirements.tools} kind="tools" /><RequirementGroup disabled={disabled} dispatch={dispatch} entries={state.spec.requirements.knowledge} kind="knowledge" /></section>;
}

function RequirementGroup({ kind, entries, dispatch, disabled }: {
  kind: RequirementKind;
  entries: Record<string, { capabilities: string[] } | { capability: string }>;
  dispatch(action: AgentEditorAction): void;
  disabled: boolean;
}) {
  const [slot, setSlot] = useState("");
  const [capability, setCapability] = useState(kind === "models" ? "chat" : "");
  const title = kind === "models" ? "Models" : kind === "tools" ? "Tools" : "Knowledge";
  function update(slotID: string, next: string) {
    if (kind === "models") dispatch({ type: "requirement.model.set", slot: slotID, capabilities: csv(next) });
    else dispatch({ type: "requirement.capability.set", kind, slot: slotID, capability: next });
  }
  return <div style={{ display: "grid", gap: 6 }}><span style={{ color: "#344054", fontSize: 10, fontWeight: 700 }}>{title}</span>{Object.entries(entries).map(([slotID, item]) => <div key={slotID} style={requirementStyle}><code>{slotID}</code><input aria-label={`${slotID} capability`} disabled={disabled} onChange={(event) => update(slotID, event.target.value)} value={"capabilities" in item ? item.capabilities.join(", ") : item.capability} /><button aria-label={`删除 ${slotID}`} disabled={disabled} onClick={() => dispatch({ type: "requirement.delete", kind, slot: slotID })} style={trashStyle}><Trash2 size={12} /></button></div>)}<div style={requirementStyle}><input aria-label={`${title} 新 Slot ID`} disabled={disabled} onChange={(event) => setSlot(event.target.value)} placeholder="slot_id" value={slot} /><input aria-label={`${title} 新 Capability`} disabled={disabled} onChange={(event) => setCapability(event.target.value)} placeholder={kind === "models" ? "chat, tool_call" : "web.search"} value={capability} /><Button aria-label={`添加 ${title} Slot`} disabled={disabled || !slot} onClick={() => { update(slot, capability); setSlot(""); setCapability(kind === "models" ? "chat" : ""); }} variant="secondary"><Plus size={12} /></Button></div></div>;
}

function generation(temperatureInput: string | number | undefined, tokenInput: string | number | undefined) {
  const temperature = temperatureInput === "" || temperatureInput === undefined ? undefined : Number(temperatureInput);
  const max_output_tokens = tokenInput === "" || tokenInput === undefined ? undefined : Number(tokenInput);
  return temperature === undefined && max_output_tokens === undefined ? undefined : { temperature, max_output_tokens };
}

function csv(value: string): string[] {
  return [...new Set(value.split(",").map((item) => item.trim()).filter(Boolean))];
}

function findSensitiveAgentSpecField(value: unknown, pointer = ""): string | null {
  if (!value || typeof value !== "object") return null;
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index += 1) {
      const found = findSensitiveAgentSpecField(value[index], `${pointer}/${index}`);
      if (found) return found;
    }
    return null;
  }
  // Keep this list in lockstep with domain.sensitiveFields. Unknown and legacy
  // fields are publication-invalid, but the backend deliberately permits them
  // at Draft L0 so users can persist incomplete work and inspect diagnostics.
  const sensitive = new Set([
    "api_key", "password", "token",
    "authorization", "credential", "client_secret",
  ]);
  for (const [field, child] of Object.entries(value as Record<string, unknown>)) {
    const fieldPointer = `${pointer}/${field}`;
    if (sensitive.has(field.toLowerCase())) return fieldPointer;
    const found = findSensitiveAgentSpecField(child, fieldPointer);
    if (found) return found;
  }
  return null;
}

const shellStyle: CSSProperties = { background: "#fff", border: "1px solid #e5eaf1", borderRadius: 11, overflow: "hidden" };
const headerStyle: CSSProperties = { alignItems: "center", borderBottom: "1px solid #e5eaf1", display: "flex", gap: 12, justifyContent: "space-between", padding: "13px 14px" };
const visualGridStyle: CSSProperties = { alignItems: "start", display: "grid", gap: 12, gridTemplateColumns: "minmax(0,1fr) minmax(270px,330px)", padding: 12 };
const emptyStyle: CSSProperties = { alignItems: "center", background: "#fff", border: "1px solid #e5eaf1", borderRadius: 11, display: "flex", flexDirection: "column", gap: 13, justifyContent: "center", minHeight: 430, padding: 30, textAlign: "center" };
const toolbarStyle: CSSProperties = { alignItems: "center", background: "#fff", border: "1px solid #e5eaf1", borderRadius: 10, display: "flex", flexWrap: "wrap", gap: 8, padding: 10 };
const panelStyle: CSSProperties = { background: "#fff", border: "1px solid #e5eaf1", borderRadius: 10, display: "grid", gap: 12, minWidth: 0, padding: 13 };
const panelHeaderStyle: CSSProperties = { alignItems: "center", borderBottom: "1px solid #edf0f4", display: "flex", justifyContent: "space-between", paddingBottom: 10 };
const fieldStyle: CSSProperties = { color: "#344054", display: "grid", fontSize: 10, fontWeight: 650, gap: 6 };
const inputStyle: CSSProperties = { background: "#fff", border: "1px solid #d8e0ea", borderRadius: 7, height: 34, minWidth: 120, padding: "0 9px" };
const requirementStyle: CSSProperties = { alignItems: "center", display: "grid", gap: 6, gridTemplateColumns: "minmax(62px,.7fr) minmax(90px,1.3fr) auto" };
const trashStyle: CSSProperties = { alignItems: "center", background: "transparent", border: 0, color: "#667085", display: "inline-flex", height: 26, justifyContent: "center", width: 26 };
const jsonStyle: CSSProperties = { background: "#0f1724", border: "1px solid #26364a", borderRadius: 9, color: "#dbeafe", font: "11px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace", minHeight: 420, padding: 15, resize: "vertical", width: "100%" };
