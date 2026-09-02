export const AGENT_SPEC_SCHEMA_VERSION = "v1" as const;

export const AGENT_NODE_KINDS = ["llm", "sequence", "parallel", "loop"] as const;

export type AgentNodeKind = (typeof AGENT_NODE_KINDS)[number];

export interface ModelRequirementV1 {
  capabilities: string[];
}

export interface CapabilityRequirementV1 {
  capability: string;
}

export interface AgentRequirementsV1 {
  models: Record<string, ModelRequirementV1>;
  tools: Record<string, CapabilityRequirementV1>;
  knowledge: Record<string, CapabilityRequirementV1>;
}

export interface GenerationOptionsV1 {
  temperature?: number;
  max_output_tokens?: number;
}

interface NamedNodeV1 {
  name?: string;
}

export interface LLMNodeV1 extends NamedNodeV1 {
  kind: "llm";
  instruction: string;
  model_slot: string;
  tool_slots: string[];
  knowledge_slots: string[];
  generation?: GenerationOptionsV1;
}

export interface SequenceNodeV1 extends NamedNodeV1 {
  kind: "sequence";
  children: string[];
}

export interface ParallelNodeV1 extends NamedNodeV1 {
  kind: "parallel";
  children: string[];
}

export interface LoopNodeV1 extends NamedNodeV1 {
  kind: "loop";
  body: string;
  max_iterations: number;
}

export type AgentNodeV1 = LLMNodeV1 | SequenceNodeV1 | ParallelNodeV1 | LoopNodeV1;

export interface AgentSpecV1 {
  schema_version: typeof AGENT_SPEC_SCHEMA_VERSION;
  root: string;
  requirements: AgentRequirementsV1;
  nodes: Record<string, AgentNodeV1>;
}

/** The API deliberately permits an empty or otherwise incomplete JSON object as a Draft. */
export type EmptyAgentSpec = Record<string, never>;
export type AgentSpecDraft = AgentSpecV1 | EmptyAgentSpec | Record<string, unknown>;

export type AgentSpecDiagnosticSeverity = "error" | "warning";

/** Structural match for the diagnostic objects returned by Control API. */
export interface AgentSpecDiagnostic {
  code: string;
  severity: AgentSpecDiagnosticSeverity;
  pointer: string;
  node_id?: string | null;
  message: string;
}

export const AGENT_SPEC_LIMITS = {
  nodes: 128,
  depth: 16,
  children: 64,
  modelSlots: 16,
  toolSlots: 64,
  knowledgeSlots: 32,
  capabilitiesPerModel: 16,
  loopIterations: 32,
  instructionBytes: 65_536,
  maxOutputTokens: 262_144,
} as const;

const IDENTIFIER_PATTERN = /^[a-z][a-z0-9_-]{0,63}$/;
const CAPABILITY_PATTERN = /^[a-z][a-z0-9_.-]{0,127}$/;

export function isAgentSpecIdentifier(value: string): boolean {
  return IDENTIFIER_PATTERN.test(value);
}

export function isAgentCapability(value: string): boolean {
  return CAPABILITY_PATTERN.test(value);
}

export function createSingleLLMAgentSpec(): AgentSpecV1 {
  return {
    schema_version: AGENT_SPEC_SCHEMA_VERSION,
    root: "assistant",
    requirements: {
      models: { primary: { capabilities: ["chat"] } },
      tools: {},
      knowledge: {},
    },
    nodes: {
      assistant: {
        kind: "llm",
        name: "通用助手",
        instruction: "准确回答用户问题。",
        model_slot: "primary",
        tool_slots: [],
        knowledge_slots: [],
      },
    },
  };
}

export function cloneAgentSpec(spec: AgentSpecV1): AgentSpecV1 {
  return JSON.parse(JSON.stringify(spec)) as AgentSpecV1;
}

export function isEmptyAgentSpec(value: unknown): value is EmptyAgentSpec {
  return isRecord(value) && Object.keys(value).length === 0;
}

export function isAgentSpecV1(value: unknown): value is AgentSpecV1 {
  return validateAgentSpecShape(value).length === 0;
}

/**
 * Mirrors the versioned JSON Schema closely enough to gate the visual and raw
 * JSON projections. Domain-semantic diagnostics live in agent-editor-state.ts.
 */
export function validateAgentSpecShape(value: unknown): AgentSpecDiagnostic[] {
  const diagnostics: AgentSpecDiagnostic[] = [];
  if (!isRecord(value)) {
    return [diagnostic("AGENT_SPEC_DOCUMENT_REQUIRED", "", "AgentSpec 顶层值必须是对象。")];
  }

  allowedFields(value, "", ["schema_version", "root", "requirements", "nodes"], diagnostics);
  requiredFields(value, "", ["schema_version", "root", "requirements", "nodes"], diagnostics);

  if ("schema_version" in value && value.schema_version !== AGENT_SPEC_SCHEMA_VERSION) {
    diagnostics.push(diagnostic("AGENT_SPEC_UNSUPPORTED_VERSION", "/schema_version", "schema_version 必须是 v1。"));
  }
  if ("root" in value) validateIdentifier(value.root, "/root", diagnostics);
  if ("requirements" in value) validateRequirements(value.requirements, diagnostics);
  if ("nodes" in value) validateNodes(value.nodes, diagnostics);

  return sortDiagnostics(diagnostics);
}

export function getNodeReferences(node: AgentNodeV1): string[] {
  if (node.kind === "sequence" || node.kind === "parallel") return node.children;
  if (node.kind === "loop") return [node.body];
  return [];
}

export function escapeJSONPointer(value: string): string {
  return value.replaceAll("~", "~0").replaceAll("/", "~1");
}

export function unescapeJSONPointer(value: string): string {
  return value.replaceAll("~1", "/").replaceAll("~0", "~");
}

export function nodeIDFromDiagnostic(diagnosticValue: Pick<AgentSpecDiagnostic, "node_id" | "pointer">): string | null {
  if (diagnosticValue.node_id) return diagnosticValue.node_id;
  const match = diagnosticValue.pointer.match(/^\/nodes\/([^/]+)/);
  return match ? unescapeJSONPointer(match[1]) : null;
}

function validateRequirements(value: unknown, diagnostics: AgentSpecDiagnostic[]): void {
  if (!isRecord(value)) {
    diagnostics.push(invalidType("/requirements"));
    return;
  }
  allowedFields(value, "/requirements", ["models", "tools", "knowledge"], diagnostics);
  requiredFields(value, "/requirements", ["models", "tools", "knowledge"], diagnostics);
  if ("models" in value) {
    validateRequirementMap(value.models, "/requirements/models", AGENT_SPEC_LIMITS.modelSlots, true, diagnostics);
  }
  if ("tools" in value) {
    validateRequirementMap(value.tools, "/requirements/tools", AGENT_SPEC_LIMITS.toolSlots, false, diagnostics);
  }
  if ("knowledge" in value) {
    validateRequirementMap(value.knowledge, "/requirements/knowledge", AGENT_SPEC_LIMITS.knowledgeSlots, false, diagnostics);
  }
}

function validateRequirementMap(
  value: unknown,
  pointer: string,
  maximum: number,
  model: boolean,
  diagnostics: AgentSpecDiagnostic[],
): void {
  if (!isRecord(value)) {
    diagnostics.push(invalidType(pointer));
    return;
  }
  if (Object.keys(value).length > maximum) diagnostics.push(limitExceeded(pointer));
  for (const [slot, requirement] of Object.entries(value)) {
    const slotPointer = `${pointer}/${escapeJSONPointer(slot)}`;
    if (!isAgentSpecIdentifier(slot)) diagnostics.push(invalidIdentifier(slotPointer));
    if (!isRecord(requirement)) {
      diagnostics.push(invalidType(slotPointer));
      continue;
    }
    if (model) {
      allowedFields(requirement, slotPointer, ["capabilities"], diagnostics);
      requiredFields(requirement, slotPointer, ["capabilities"], diagnostics);
      if ("capabilities" in requirement) {
        validateStringArray(
          requirement.capabilities,
          `${slotPointer}/capabilities`,
          1,
          AGENT_SPEC_LIMITS.capabilitiesPerModel,
          isAgentCapability,
          diagnostics,
        );
      }
      continue;
    }
    allowedFields(requirement, slotPointer, ["capability"], diagnostics);
    requiredFields(requirement, slotPointer, ["capability"], diagnostics);
    if ("capability" in requirement) validateCapability(requirement.capability, `${slotPointer}/capability`, diagnostics);
  }
}

function validateNodes(value: unknown, diagnostics: AgentSpecDiagnostic[]): void {
  if (!isRecord(value)) {
    diagnostics.push(invalidType("/nodes"));
    return;
  }
  const entries = Object.entries(value);
  if (entries.length === 0 || entries.length > AGENT_SPEC_LIMITS.nodes) diagnostics.push(limitExceeded("/nodes"));
  for (const [nodeID, candidate] of entries) {
    const pointer = `/nodes/${escapeJSONPointer(nodeID)}`;
    if (!isAgentSpecIdentifier(nodeID)) diagnostics.push(invalidIdentifier(pointer));
    if (!isRecord(candidate)) {
      diagnostics.push(invalidType(pointer));
      continue;
    }
    validateNode(candidate, pointer, nodeID, diagnostics);
  }
}

function validateNode(node: Record<string, unknown>, pointer: string, nodeID: string, diagnostics: AgentSpecDiagnostic[]): void {
  if (!("kind" in node)) {
    diagnostics.push(requiredField(`${pointer}/kind`));
    return;
  }
  if (typeof node.kind !== "string") {
    diagnostics.push(invalidType(`${pointer}/kind`));
    return;
  }
  if (!AGENT_NODE_KINDS.includes(node.kind as AgentNodeKind)) {
    diagnostics.push(
      diagnostic("AGENT_SPEC_UNSUPPORTED_NODE_KIND", `${pointer}/kind`, "节点类型不属于 AgentSpec V1。", nodeID),
    );
    return;
  }

  if (node.kind === "llm") {
    allowedFields(node, pointer, ["kind", "name", "instruction", "model_slot", "tool_slots", "knowledge_slots", "generation"], diagnostics);
    requiredFields(node, pointer, ["kind", "instruction", "model_slot", "tool_slots", "knowledge_slots"], diagnostics);
    validateOptionalName(node, pointer, diagnostics);
    if ("instruction" in node) {
      if (typeof node.instruction !== "string") diagnostics.push(invalidType(`${pointer}/instruction`, nodeID));
      else if (node.instruction.trim() === "" || utf8Length(node.instruction) > AGENT_SPEC_LIMITS.instructionBytes) {
        diagnostics.push(limitExceeded(`${pointer}/instruction`, nodeID));
      }
    }
    if ("model_slot" in node) validateIdentifier(node.model_slot, `${pointer}/model_slot`, diagnostics, nodeID);
    if ("tool_slots" in node) {
      validateStringArray(node.tool_slots, `${pointer}/tool_slots`, 0, AGENT_SPEC_LIMITS.toolSlots, isAgentSpecIdentifier, diagnostics, nodeID);
    }
    if ("knowledge_slots" in node) {
      validateStringArray(
        node.knowledge_slots,
        `${pointer}/knowledge_slots`,
        0,
        AGENT_SPEC_LIMITS.knowledgeSlots,
        isAgentSpecIdentifier,
        diagnostics,
        nodeID,
      );
    }
    if ("generation" in node) validateGeneration(node.generation, `${pointer}/generation`, diagnostics, nodeID);
    return;
  }

  if (node.kind === "sequence" || node.kind === "parallel") {
    allowedFields(node, pointer, ["kind", "name", "children"], diagnostics);
    requiredFields(node, pointer, ["kind", "children"], diagnostics);
    validateOptionalName(node, pointer, diagnostics);
    if ("children" in node) {
      validateStringArray(node.children, `${pointer}/children`, 1, AGENT_SPEC_LIMITS.children, isAgentSpecIdentifier, diagnostics, nodeID);
    }
    return;
  }

  allowedFields(node, pointer, ["kind", "name", "body", "max_iterations"], diagnostics);
  requiredFields(node, pointer, ["kind", "body", "max_iterations"], diagnostics);
  validateOptionalName(node, pointer, diagnostics);
  if ("body" in node) validateIdentifier(node.body, `${pointer}/body`, diagnostics, nodeID);
  if ("max_iterations" in node) {
    if (!Number.isInteger(node.max_iterations)) diagnostics.push(invalidType(`${pointer}/max_iterations`, nodeID));
    else if ((node.max_iterations as number) < 1 || (node.max_iterations as number) > AGENT_SPEC_LIMITS.loopIterations) {
      diagnostics.push(limitExceeded(`${pointer}/max_iterations`, nodeID));
    }
  }
}

function validateOptionalName(node: Record<string, unknown>, pointer: string, diagnostics: AgentSpecDiagnostic[]): void {
  if (!("name" in node)) return;
  if (typeof node.name !== "string") diagnostics.push(invalidType(`${pointer}/name`));
  else if (node.name.trim() === "" || Array.from(node.name).length > 128) diagnostics.push(limitExceeded(`${pointer}/name`));
}

function validateGeneration(value: unknown, pointer: string, diagnostics: AgentSpecDiagnostic[], nodeID: string): void {
  if (!isRecord(value)) {
    diagnostics.push(invalidType(pointer, nodeID));
    return;
  }
  allowedFields(value, pointer, ["temperature", "max_output_tokens"], diagnostics);
  if ("temperature" in value) {
    if (typeof value.temperature !== "number" || !Number.isFinite(value.temperature)) diagnostics.push(invalidType(`${pointer}/temperature`, nodeID));
    else if (value.temperature < 0 || value.temperature > 2) diagnostics.push(limitExceeded(`${pointer}/temperature`, nodeID));
  }
  if ("max_output_tokens" in value) {
    if (!Number.isInteger(value.max_output_tokens)) diagnostics.push(invalidType(`${pointer}/max_output_tokens`, nodeID));
    else if ((value.max_output_tokens as number) < 1 || (value.max_output_tokens as number) > AGENT_SPEC_LIMITS.maxOutputTokens) {
      diagnostics.push(limitExceeded(`${pointer}/max_output_tokens`, nodeID));
    }
  }
}

function validateStringArray(
  value: unknown,
  pointer: string,
  minimum: number,
  maximum: number,
  predicate: (item: string) => boolean,
  diagnostics: AgentSpecDiagnostic[],
  nodeID?: string,
): void {
  if (!Array.isArray(value)) {
    diagnostics.push(invalidType(pointer, nodeID));
    return;
  }
  if (value.length < minimum || value.length > maximum) diagnostics.push(limitExceeded(pointer, nodeID));
  const seen = new Set<string>();
  value.forEach((item, index) => {
    const itemPointer = `${pointer}/${index}`;
    if (typeof item !== "string") diagnostics.push(invalidType(itemPointer, nodeID));
    else {
      if (!predicate(item)) diagnostics.push(invalidIdentifier(itemPointer, nodeID));
      if (seen.has(item)) {
        const duplicateCode = pointer.endsWith("/children")
          ? "AGENT_SPEC_DUPLICATE_CHILD"
          : "AGENT_SPEC_LIMIT_EXCEEDED";
        diagnostics.push(diagnostic(duplicateCode, itemPointer, "数组元素必须唯一。", nodeID));
      }
      seen.add(item);
    }
  });
}

function validateIdentifier(value: unknown, pointer: string, diagnostics: AgentSpecDiagnostic[], nodeID?: string): void {
  if (typeof value !== "string") diagnostics.push(invalidType(pointer, nodeID));
  else if (!isAgentSpecIdentifier(value)) diagnostics.push(invalidIdentifier(pointer, nodeID));
}

function validateCapability(value: unknown, pointer: string, diagnostics: AgentSpecDiagnostic[]): void {
  if (typeof value !== "string") diagnostics.push(invalidType(pointer));
  else if (!isAgentCapability(value)) diagnostics.push(invalidIdentifier(pointer));
}

function allowedFields(
  value: Record<string, unknown>,
  pointer: string,
  allowed: readonly string[],
  diagnostics: AgentSpecDiagnostic[],
): void {
  for (const field of Object.keys(value)) {
    if (!allowed.includes(field)) {
      diagnostics.push(
        diagnostic(
          "AGENT_SPEC_UNKNOWN_FIELD",
          `${pointer}/${escapeJSONPointer(field)}`,
          "字段不属于 AgentSpec V1。",
        ),
      );
    }
  }
}

function requiredFields(
  value: Record<string, unknown>,
  pointer: string,
  required: readonly string[],
  diagnostics: AgentSpecDiagnostic[],
): void {
  for (const field of required) {
    if (!(field in value)) diagnostics.push(requiredField(`${pointer}/${field}`));
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

function diagnostic(
  code: string,
  pointer: string,
  message: string,
  nodeID: string | null = null,
  severity: AgentSpecDiagnosticSeverity = "error",
): AgentSpecDiagnostic {
  return { code, severity, pointer, node_id: nodeID, message };
}

function invalidType(pointer: string, nodeID?: string): AgentSpecDiagnostic {
  return diagnostic("AGENT_SPEC_INVALID_TYPE", pointer, "字段 JSON 类型无效。", nodeID ?? null);
}

function invalidIdentifier(pointer: string, nodeID?: string): AgentSpecDiagnostic {
  return diagnostic("AGENT_SPEC_INVALID_IDENTIFIER", pointer, "标识符不符合 V1 格式。", nodeID ?? null);
}

function requiredField(pointer: string): AgentSpecDiagnostic {
  return diagnostic("AGENT_SPEC_REQUIRED_FIELD", pointer, "缺少 AgentSpec V1 必填字段。");
}

function limitExceeded(pointer: string, nodeID?: string): AgentSpecDiagnostic {
  return diagnostic("AGENT_SPEC_LIMIT_EXCEEDED", pointer, "字段超出 AgentSpec V1 限制。", nodeID ?? null);
}

function sortDiagnostics(diagnostics: AgentSpecDiagnostic[]): AgentSpecDiagnostic[] {
  return diagnostics.sort((left, right) =>
    left.pointer.localeCompare(right.pointer) ||
    left.severity.localeCompare(right.severity) ||
    left.code.localeCompare(right.code),
  );
}
