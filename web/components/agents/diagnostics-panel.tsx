"use client";

import type { CSSProperties } from "react";
import { AlertCircle, AlertTriangle, CheckCircle2 } from "lucide-react";

import { nodeIDFromDiagnostic, type AgentSpecDiagnostic } from "../../lib/agent-spec-v1";

export function DiagnosticsPanel({
  diagnostics,
  onSelectNode,
}: {
  diagnostics: readonly AgentSpecDiagnostic[];
  onSelectNode?: (nodeID: string) => void;
}) {
  if (diagnostics.length === 0) {
    return (
      <div style={{ ...panelStyle, color: "#157347", background: "#f0fbf5" }} data-testid="diagnostics-empty">
        <CheckCircle2 size={16} />
        <span>本地结构检查通过；发布仍以服务端校验结果为准。</span>
      </div>
    );
  }

  return (
    <section aria-label="AgentSpec 诊断" style={panelStyle}>
      <header style={{ display: "flex", justifyContent: "space-between", gap: 12, alignItems: "center" }}>
        <strong style={{ fontSize: 13 }}>诊断</strong>
        <span style={{ color: "#667085", fontSize: 11 }}>
          {diagnostics.filter((item) => item.severity === "error").length} 个错误 · {diagnostics.filter((item) => item.severity === "warning").length} 个警告
        </span>
      </header>
      <div style={{ display: "grid", gap: 8, marginTop: 10 }}>
        {diagnostics.map((item, index) => {
          const nodeID = nodeIDFromDiagnostic(item);
          const warning = item.severity === "warning";
          const content = (
            <>
              {warning ? <AlertTriangle size={15} /> : <AlertCircle size={15} />}
              <span style={{ minWidth: 0, flex: 1 }}>
                <strong style={{ display: "block", fontSize: 11 }}>{item.message}</strong>
                <code style={{ display: "block", color: "#778292", fontSize: 9, marginTop: 3, overflowWrap: "anywhere" }}>
                  {item.code} · {item.pointer || "/"}
                </code>
              </span>
            </>
          );
          const style: CSSProperties = {
            alignItems: "flex-start",
            background: warning ? "#fff8e8" : "#fff2f3",
            border: `1px solid ${warning ? "#f7dfaa" : "#ffd4d9"}`,
            borderRadius: 8,
            color: warning ? "#8a5600" : "#a72d3a",
            display: "flex",
            gap: 8,
            padding: "9px 10px",
            textAlign: "left",
            width: "100%",
          };
          return nodeID && onSelectNode ? (
            <button key={`${item.code}-${item.pointer}-${index}`} type="button" style={style} onClick={() => onSelectNode(nodeID)}>
              {content}
            </button>
          ) : (
            <div key={`${item.code}-${item.pointer}-${index}`} style={style}>{content}</div>
          );
        })}
      </div>
    </section>
  );
}

const panelStyle: CSSProperties = {
  alignItems: "center",
  background: "#fff",
  border: "1px solid #e5eaf1",
  borderRadius: 10,
  display: "flex",
  flexDirection: "column",
  gap: 6,
  padding: 12,
};
