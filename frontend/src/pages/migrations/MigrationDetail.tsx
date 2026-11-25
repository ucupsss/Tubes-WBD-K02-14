import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, useExecutionDetail, useMigration, useRecentExecutions } from '../../api/hooks'
import type { ExecutionItem, MigrationItem } from '../../api/types'
import { Alert, Badge, ConfirmButton, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { formatDateTime, useSubmit } from '../../lib/form'
import { statusBadgeTone, statusLabel } from '../executions/ExecutionHistory'
import { MigrationExplainRunner } from '../explorer/ExplainView'
import { Breadcrumb, useCurrentDatabase } from './MigrationList'
import { executionsOf, migrationState, workflowStep, WORKFLOW_STEPS } from './status'

const KIND_LABEL: Record<ExecutionItem['kind'], string> = { apply: 'Apply', forward: 'Forward', rollback: 'Rollback' }

/** Halaman detail migration (/db/:dbID/migrations/:id) — paket A6. */
export function MigrationDetailPage() {
  const params = useParams()
  const dbId = Number(params.dbID)
  const id = Number(params.id)
  const query = useMigration(dbId, id)
  return <QueryState query={query}>{m => <Detail dbId={dbId} m={m} />}</QueryState>
}

function Detail({ dbId, m }: { dbId: number; m: MigrationItem }) {
  const db = useCurrentDatabase(dbId)
  const executions = useRecentExecutions(dbId)
  const navigate = useNavigate()
  const flash = useFlash()
  const action = useSubmit()
  const [showExplain, setShowExplain] = useState(false)
  const execItems = executions.data?.items ?? []
  const st = migrationState(m, execItems)
  const mine = executionsOf(m.id, execItems)
  const canEdit = db?.level === 'edit'
  const rev = m.revision
  const invalidate = [keys.migrations(dbId), keys.executionsAll(dbId), keys.approvals]

  const submit = useAction(() => api.post<ExecutionItem>(`/api/databases/${dbId}/migrations/${m.id}/submit`, {}), invalidate)
  const remove = useAction(() => api.del(`/api/databases/${dbId}/migrations/${m.id}`), invalidate)

  const onSubmit = async () => {
    const out: { exec?: ExecutionItem } = {}
    if (await action.run(async () => (out.exec = await submit.mutateAsync()))) {
      flash.success('Migration diajukan. Menunggu approval leader setiap group yang punya akses.')
      if (out.exec) {
        navigate(`/executions/${out.exec.id}`)
      }
    }
  }
  const onDelete = async () => {
    if (await action.run(() => remove.mutateAsync())) {
      flash.success(`Draft "${m.title}" dihapus`)
      navigate(`/db/${dbId}/migrations`)
    }
  }

  return (
    <>
      <Breadcrumb dbId={dbId} dbName={db?.name}>
        <Link to={`/db/${dbId}/migrations`}>Migration</Link> <span>/</span> <strong>#{m.id}</strong>
      </Breadcrumb>
      <PageHeader
        title={m.title}
        actions={
          canEdit && (
            <>
              {!st.open && (
                <Link className="btn btn-ghost" to={`/db/${dbId}/migrations/${m.id}/edit`}>
                  {st.frozen ? 'Buat revisi baru' : 'Ubah'}
                </Link>
              )}
              {!st.frozen && (
                <button type="button" className="btn btn-ghost" onClick={() => setShowExplain(v => !v)}>
                  {showExplain ? 'Tutup EXPLAIN' : 'Analisis EXPLAIN'}
                </button>
              )}
              {st.canDelete && <ConfirmButton label="Hapus draft" confirmLabel="Ya, hapus" onConfirm={() => void onDelete()} />}
              {st.canSubmit && (
                <button type="button" className="btn btn-primary" disabled={action.pending} onClick={() => void onSubmit()}>
                  {action.pending ? 'Mengajukan…' : 'Submit for review'}
                </button>
              )}
            </>
          )
        }
      />
      {action.error && <Alert>{action.error}</Alert>}

      <div className="card">
        <div className="page-header">
          <h2>Status</h2>
          <Badge tone={st.tone}>{st.label}</Badge>
        </div>
        <Workflow latest={st.latest} frozen={st.frozen} />
        {st.latest && (
          <p className="small">
            Eksekusi terakhir:{' '}
            <Link to={`/executions/${st.latest.id}`}>
              #{st.latest.id} · {KIND_LABEL[st.latest.kind]}
            </Link>{' '}
            <Badge tone={statusBadgeTone(st.latest.status)}>{statusLabel(st.latest.status)}</Badge>
          </p>
        )}
        {st.latest?.status === 'pending_review' && <PendingApprovals execId={st.latest.id} />}
        {st.latest?.status === 'approved' && (
          <Alert kind="info">
            Semua leader sudah menyetujui. Buka{' '}
            <Link to={`/executions/${st.latest.id}`}>execution #{st.latest.id}</Link> lalu tekan <strong>Jalankan</strong>.
          </Alert>
        )}
      </div>

      {canEdit && showExplain && <MigrationExplainRunner dbId={dbId} initialMigrationId={m.id} />}

      <div className="card">
        <h2>Revisi {rev.revision_no}</h2>
        <dl className="kv">
          <dt>Mode</dt>
          <dd>
            {rev.exec_mode === 'batch'
              ? `Batch · ${rev.batch_table}.${rev.batch_column} · ${rev.batch_size?.toLocaleString('id-ID')} per batch`
              : 'Normal (satu transaction)'}
          </dd>
          <dt>Versi aktif</dt>
          <dd>{m.version ?? '—'}</dd>
          <dt>Diajukan</dt>
          <dd>{rev.frozen_at ? `${formatDateTime(rev.frozen_at)} (revisi terkunci)` : 'Belum (masih draft)'}</dd>
          <dt>Dibuat</dt>
          <dd>
            {m.created_by_username} · {formatDateTime(m.created_at)}
          </dd>
          <dt>Hash SQL</dt>
          <dd>
            <code className="small">{rev.sql_hash.slice(0, 16)}</code>
          </dd>
        </dl>
        <h3>SQL up</h3>
        <pre className="sql-box">{rev.up_sql}</pre>
        {rev.irreversible ? (
          <Alert kind="info">
            <strong>Irreversible.</strong> {rev.irreversible_reason}
          </Alert>
        ) : (
          <>
            <h3>SQL down</h3>
            <pre className="sql-box">{rev.down_sql}</pre>
          </>
        )}
      </div>

      <div className="card">
        <div className="page-header">
          <h2>Riwayat eksekusi migration ini</h2>
          <Link className="btn btn-ghost" to={`/db/${dbId}/executions`}>
            Semua riwayat
          </Link>
        </div>
        {mine.length === 0 ? (
          <p className="muted">Belum pernah diajukan.</p>
        ) : (
          <TableWrap>
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Jenis</th>
                  <th>Status</th>
                  <th>Ronde</th>
                  <th>Diajukan</th>
                </tr>
              </thead>
              <tbody>
                {mine.map(e => (
                  <tr key={e.id}>
                    <td data-label="ID">
                      <Link to={`/executions/${e.id}`}>#{e.id}</Link>
                    </td>
                    <td data-label="Jenis">{KIND_LABEL[e.kind]}</td>
                    <td data-label="Status">
                      <Badge tone={statusBadgeTone(e.status)}>{statusLabel(e.status)}</Badge>
                    </td>
                    <td data-label="Ronde">{e.approval_round}</td>
                    <td data-label="Diajukan">
                      {e.requested_by_username ?? `#${e.requested_by}`}
                      <div className="muted small">{formatDateTime(e.created_at)}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableWrap>
        )}
      </div>
    </>
  )
}

function Workflow({ latest, frozen }: { latest: ExecutionItem | null; frozen: boolean }) {
  const { index, failed } = workflowStep(latest, frozen)
  return (
    <ol className="workflow" aria-label="Alur kerja migration">
      {WORKFLOW_STEPS.map((step, i) => {
        const state = i < index ? 'done' : i === index ? (failed ? 'failed' : 'current') : 'todo'
        return (
          <li key={step} className={`workflow-step is-${state}`} aria-current={i === index ? 'step' : undefined}>
            <span className="workflow-dot" aria-hidden="true">
              {state === 'done' ? '✓' : state === 'failed' ? '✕' : i + 1}
            </span>
            <span>{step}</span>
          </li>
        )
      })}
    </ol>
  )
}

/** Keputusan leader per group pada ronde approval yang sedang berjalan. */
function PendingApprovals({ execId }: { execId: number }) {
  const detail = useExecutionDetail(execId)
  const items = detail.data?.approvals ?? []
  const round = detail.data?.execution.approval_round
  const current = items.filter(a => a.round === round)
  if (detail.isPending || current.length === 0) {
    return null
  }
  return (
    <TableWrap>
      <table>
        <thead>
          <tr>
            <th>Group</th>
            <th>Leader</th>
            <th>Keputusan</th>
          </tr>
        </thead>
        <tbody>
          {current.map(a => (
            <tr key={a.group_id}>
              <td data-label="Group">{a.group_name}</td>
              <td data-label="Leader">{a.leader_name || <span className="muted">Belum ada leader</span>}</td>
              <td data-label="Keputusan">
                <Badge tone={a.decision === 'approved' ? 'ok' : a.decision === 'rejected' ? 'danger' : 'warn'}>
                  {a.decision === 'approved' ? 'Disetujui' : a.decision === 'rejected' ? 'Ditolak' : 'Menunggu'}
                </Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableWrap>
  )
}
