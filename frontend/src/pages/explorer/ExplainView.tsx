import { useState } from 'react'
import { runMigrationExplain } from '../../api/hooks'
import type { ExplainNode, ExplainReport, ExplainStatementResult } from '../../api/types'
import { Alert, Badge, Field } from '../../components/ui'
import { useSubmit } from '../../lib/form'

/**
 * Komponen hasil analisis EXPLAIN / EXPLAIN ANALYZE untuk satu laporan.
 * Dapat dipakai baik di panel interaktif Explorer maupun di halaman detail migration.
 */
export function ExplainReportView({ report }: { report: ExplainReport }) {
  return (
    <div className="explain-report">
      <div className="card">
        <div className="page-header">
          <h2>Hasil Analisis ({report.analyze ? 'EXPLAIN ANALYZE' : 'EXPLAIN'})</h2>
          <div className="form-actions">
            <Badge tone={report.failed > 0 ? 'danger' : 'ok'}>
              {report.statements.length} statement · {report.failed} gagal
            </Badge>
            <Badge tone="info">Seluruh perubahan sudah di-rollback</Badge>
          </div>
        </div>
        <p className="muted small">
          Node dengan waktu eksklusif ≥ 30% (min. 1 ms) ditandai <strong>Lambat</strong>. Node dengan selisih estimasi
          vs baris aktual ≥ 10× ditandai <strong>Meleset</strong>.
        </p>
      </div>

      {report.statements.map(st => (
        <StatementCard key={st.index} st={st} />
      ))}
    </div>
  )
}

function StatementCard({ st }: { st: ExplainStatementResult }) {
  const p = st.plan
  return (
    <div className="card">
      <div className="page-header">
        <h3>
          Statement #{st.index} <Badge tone="neutral">{st.kind.toUpperCase()}</Badge>
        </h3>
        {p && (
          <div className="form-actions">
            <Badge tone="info">Planning: {p.planning_ms.toFixed(2)} ms</Badge>
            {p.analyzed && <Badge tone="ok">Execution: {p.execution_ms.toFixed(2)} ms</Badge>}
            {p.slow_nodes > 0 && <Badge tone="warn">{p.slow_nodes} node lambat</Badge>}
            {p.misestimates > 0 && <Badge tone="danger">{p.misestimates} estimasi meleset</Badge>}
          </div>
        )}
      </div>

      <pre className="sql-box">{st.sql}</pre>

      {st.error && <Alert kind="error">Gagal: {st.error}</Alert>}
      {st.note && <Alert kind="info">{st.note}</Alert>}

      {p && (
        <>
          {(p.scan_summary?.length > 0 || p.join_summary?.length > 0) && (
            <div className="plan-summary">
              {p.scan_summary?.map((s, idx) => (
                <Badge key={`scan-${idx}`} tone="neutral">
                  Scan: {s}
                </Badge>
              ))}
              {p.join_summary?.map((j, idx) => (
                <Badge key={`join-${idx}`} tone="info">
                  Join: {j}
                </Badge>
              ))}
            </div>
          )}
          <div className="plan-tree">
            <PlanNodeTree node={p.root} analyzed={p.analyzed} depth={0} />
          </div>
        </>
      )}
    </div>
  )
}

function PlanNodeTree({ node, analyzed, depth }: { node: ExplainNode; analyzed: boolean; depth: number }) {
  const titleParts = [node.node_type]
  if (node.operation) {
    titleParts.push(`(${node.operation})`)
  }
  if (node.relation_name) {
    const rel = node.schema ? `${node.schema}.${node.relation_name}` : node.relation_name
    titleParts.push(`on ${rel}`)
  }
  if (node.index_name) {
    titleParts.push(`using ${node.index_name}`)
  }

  return (
    <div className="plan-node" style={{ marginLeft: depth * 16 }}>
      <div className="plan-node-header">
        <strong>{titleParts.join(' ')}</strong>
        <span className="muted small">
          cost={node.startup_cost.toFixed(2)}..{node.total_cost.toFixed(2)} rows={node.plan_rows} width=
          {node.plan_width}
        </span>
        {analyzed && (
          <span className="small">
            actual={node.actual_total_time.toFixed(2)} ms (excl. {node.exclusive_ms.toFixed(2)} ms) · rows=
            {node.actual_rows} · loops={node.actual_loops}
          </span>
        )}
        {node.slow && <Badge tone="warn">Lambat ({node.exclusive_ms.toFixed(2)} ms)</Badge>}
        {node.misestimated && <Badge tone="danger">Meleset ({node.estimate_ratio.toFixed(1)}×)</Badge>}
      </div>
      {node.condition && <div className="plan-cond small muted">Kondisi: {node.condition}</div>}
      {node.rows_removed > 0 && <div className="plan-cond small muted">Rows removed by filter: {node.rows_removed}</div>}
      {node.children?.map((child, idx) => (
        <PlanNodeTree key={idx} node={child} analyzed={analyzed} depth={depth + 1} />
      ))}
    </div>
  )
}

/**
 * Widget untuk menjalankan EXPLAIN / EXPLAIN ANALYZE terhadap ID migration tertentu pada database.
 */
export function MigrationExplainRunner({
  dbId,
  initialMigrationId,
}: {
  dbId: number
  initialMigrationId?: number
}) {
  const [migIdStr, setMigIdStr] = useState(initialMigrationId ? String(initialMigrationId) : '')
  const [analyze, setAnalyze] = useState(false)
  const [report, setReport] = useState<ExplainReport | null>(null)
  const submit = useSubmit()

  const onRun = async (e: React.FormEvent) => {
    e.preventDefault()
    const migId = Number(migIdStr)
    if (!Number.isFinite(migId) || migId <= 0) {
      return
    }
    await submit.run(async () => {
      const res = await runMigrationExplain(dbId, migId, analyze)
      setReport(res)
    })
  }

  return (
    <div className="card">
      <h2>Analisis Rencana Eksekusi (EXPLAIN)</h2>
      <p className="muted small">
        Jalankan EXPLAIN atau EXPLAIN ANALYZE terhadap migration berstatus draft/gagal dalam transaksi yang otomatis
        di-rollback.
      </p>
      <form onSubmit={e => void onRun(e)} className="form-row">
        <Field label="ID Migration" error={submit.fields.id}>
          <input
            type="number"
            min={1}
            placeholder="Contoh: 1"
            value={migIdStr}
            onChange={e => setMigIdStr(e.target.value)}
            required
          />
        </Field>
        <label className="check">
          <input type="checkbox" checked={analyze} onChange={e => setAnalyze(e.target.checked)} />
          <span>EXPLAIN ANALYZE (eksekusi nyata + rollback)</span>
        </label>
        <button type="submit" className="btn btn-primary" disabled={submit.pending}>
          {submit.pending ? 'Menganalisis…' : 'Jalankan Analisis'}
        </button>
      </form>
      {submit.error && <Alert kind="error">{submit.error}</Alert>}
      {report && <ExplainReportView report={report} />}
    </div>
  )
}
