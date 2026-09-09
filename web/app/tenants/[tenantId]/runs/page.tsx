"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

import { ApiNotice, Button, EmptyState, PageHeader, StatusBadge } from "../../../../components/ui";
import { controlApi, type RunSummary } from "../../../../lib/control-api";

const PAGE_SIZE = 25;

function tone(status: string): "green" | "blue" | "amber" | "gray" {
  if (status === "SUCCEEDED" || status === "COMPLETED") return "green";
  if (status === "FAILED") return "amber";
  if (status === "RUNNING" || status === "EXECUTION") return "blue";
  return "gray";
}

export default function RunsPage() {
  const { tenantId } = useParams<{ tenantId: string }>();
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [offset, setOffset] = useState(0);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>();
  const load = useCallback(async () => {
    setLoading(true); setError(undefined);
    try { const result = await controlApi.listRuns(tenantId, { offset, limit: PAGE_SIZE }); setRuns(result.runs); setTotal(result.total); }
    catch (caught) { setError(caught); }
    finally { setLoading(false); }
  }, [offset, tenantId]);
  useEffect(() => { void load(); }, [load]);
  return <>
    <PageHeader eyebrow="RUNTIME OPERATIONS" title="运行记录" description="按业务 Run 查看排队、执行、Memory 提交与回复交接状态；无需直接查询 Worker 数据库。" />
    <ApiNotice error={error} />
    <div className="panel">{loading ? <div className="panel-loading"><div className="loading-mark" /><span>正在读取运行事实…</span></div> : runs.length === 0 ? <EmptyState title="暂无运行记录" detail="渠道接收并成功创建 Run 后会显示在这里。" /> : <div className="table-wrap"><table><thead><tr><th>Run</th><th>当前阶段</th><th>状态</th><th>尝试</th><th>Token</th><th>接收时间</th><th>操作</th></tr></thead><tbody>{runs.map((run) => <tr key={run.run_id}><td><strong>{run.run_id}</strong><br/><small>Session {run.session_id}</small></td><td><StatusBadge tone={tone(run.stage)}>{run.stage}</StatusBadge></td><td>{run.status}{run.failure_reason ? <><br/><small>{run.failure_reason}</small></> : null}</td><td>{run.attempts}</td><td>{run.total_tokens.toLocaleString("zh-CN")}</td><td>{new Date(run.accepted_at).toLocaleString("zh-CN")}</td><td><Link className="text-link" href={`/tenants/${encodeURIComponent(tenantId)}/runs/${encodeURIComponent(run.run_id)}`}>查看过程 →</Link></td></tr>)}</tbody></table></div>}
    {!loading && total > 0 && <div className="pagination"><span>第 {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} 条，共 {total} 条</span><div className="pagination-actions"><Button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} variant="secondary"><ChevronLeft size={14}/>上一页</Button><Button disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)} variant="secondary">下一页<ChevronRight size={14}/></Button></div></div>}</div>
  </>;
}
