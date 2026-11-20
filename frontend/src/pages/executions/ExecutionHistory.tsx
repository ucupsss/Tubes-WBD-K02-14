import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, useDatabaseExecutions, useDatabaseSchema } from '../../api/hooks'
import type { AsyncJobCreated, ExecutionItem, ExecutionStatus } from '../../api/types'
import { Alert, Badge, ConfirmButton, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { formatDateTime, useSubmit } from '../../lib/form'

export function statusBadgeTone(status: ExecutionStatus): 'neutral' | 'ok' | 'warn' | 'danger' | 'info' {
  switch (status) {
    case 'success':
    case 'approved':
      return 'ok'
    case 'running':
    case 'queued':
      return 'info'
    case 'partial':
    case 'partially_reverted':
    case 'uncertain':
    case 'pending_review':
      return 'warn'
    case 'failed':
    case 'rejected':
      return 'danger'
    default:
      return 'neutral'
  }
}

export function statusLabel(status: ExecutionStatus): string {
  switch (status) {
    case 'pending_review':
      return 'Pending Review'
    case 'approved':
      return 'Approved'
    case 'queued':
      return 'Queued'
    case 'running':
      return 'Running'
    case 'success':
      return 'Success'
    case 'failed':
      return 'Failed'
    case 'partial':
      return 'Partial (Batch)'
    case 'partially_reverted':
      return 'Partially Reverted'
    case 'uncertain':
      return 'Uncertain'
    case 'rejected':
      return 'Rejected'
    case 'cancelled':
      return 'Cancelled'
    default:
      return status
  }
}

/**
 * Halaman Riwayat Eksekusi Database (/db/:dbID/executions) — Paket B5.
 * Menyediakan filter checkbox "Hide rollback" (D33, default tidak dicentang) yang menyembunyikan
 * pasangan execution yang dibatalkan dan execution rollback sukses berdasarkan Execution ID.
 */
export function ExecutionHistoryPage() {
  const { dbID } = useParams<{ dbID: string }>()
  const id = Number(dbID)
  // Default awal: checkbox TIDAK dicentang (tampilkan semua) sesuai panduan 7.1.
  const [hideRollback, setHideRollback] = useState(false)
  const schemaQuery = useDatabaseSchema(id)
  const execQuery = useDatabaseExecutions(id, hideRollback)

  const dbName = schemaQuery.data?.database.name ?? `Database #${id}`
  const canEdit = schemaQuery.data?.database.level === 'edit'

  return (
    <>
      <div className="breadcrumb small muted">
        <Link to="/">Dashboard</Link> <span>/</span> <Link to={`/db/${id}`}>{dbName}</Link> <span>/</span>{' '}
        <strong>Riwayat Eksekusi</strong>
      </div>

      <PageHeader
        title={`${dbName} · Riwayat Eksekusi`}
        actions={
          <div className="form-actions">
            <Link className="btn btn-ghost" to={`/db/${id}`}>
              Katalog Skema
            </Link>
            <a className="btn btn-ghost" href={`/db/${id}/migrations`}>
              Daftar Migration
            </a>
          </div>
        }
      />

      <div className="card">
        <div className="page-header">
          <label className="check">
            <input
              type="checkbox"
              checked={hideRollback}
              onChange={e => setHideRollback(e.target.checked)}
            />
            <span>
              <strong>Hide rollback</strong> — Sembunyikan pasangan eksekusi yang telah di-rollback sukses beserta
              eksekusi rollback-nya (berdasarkan Execution ID)
            </span>
          </label>
          <button type="button" className="btn btn-ghost" onClick={() => void execQuery.refetch()}>
            Segarkan
          </button>
        </div>
        <p className="muted small">
          Semua percobaan (sukses maupun gagal) selalu dicatat dan tidak pernah dihapus. Filter ini hanya mengubah
          tampilan.
        </p>
      </div>

      <QueryState query={execQuery}>
        {({ items }) => {
          // Hitung execution teratas pada rantai aktif bila backend tidak mengirim field is_top_of_chain secara eksplisit.
          const rolledBackIds = new Set<number>()
          for (const e of items) {
            if (e.kind === 'rollback' && e.status === 'success' && e.rolls_back_execution_id) {
              rolledBackIds.add(e.rolls_back_execution_id)
            }
          }
          let topActiveExecId: number | null = null
          for (const e of items) {
            if ((e.kind === 'apply' || e.kind === 'forward') && e.status === 'success' && !rolledBackIds.has(e.id)) {
              if (topActiveExecId === null || e.id > topActiveExecId) {
                topActiveExecId = e.id
              }
            }
          }

          return items.length === 0 ? (
            <div className="card empty">
              <p>
                {hideRollback
                  ? 'Tidak ada riwayat eksekusi aktif yang ditampilkan saat filter Hide rollback menyala.'
                  : 'Belum ada riwayat eksekusi migration pada database ini.'}
              </p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Exec ID</th>
                    <th>Versi</th>
                    <th>Migration & Revisi</th>
                    <th>Jenis</th>
                    <th>Status</th>
                    <th>Progress Batch</th>
                    <th>Pengaju & Waktu</th>
                    <th>
                      <span className="sr-only">Aksi</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map(item => (
                    <ExecutionRow
                      key={item.id}
                      dbId={id}
                      item={item}
                      canEdit={canEdit}
                      isTopActive={item.is_top_of_chain ?? item.id === topActiveExecId}
                      isRolledBack={rolledBackIds.has(item.id)}
                      hideRollback={hideRollback}
                    />
                  ))}
                </tbody>
              </table>
            </TableWrap>
          )
        }}
      </QueryState>
    </>
  )
}

function ExecutionRow({
  dbId,
  item,
  canEdit,
  isTopActive,
  isRolledBack,
  hideRollback,
}: {
  dbId: number
  item: ExecutionItem
  canEdit: boolean
  isTopActive: boolean
  isRolledBack: boolean
  hideRollback: boolean
}) {
  const flash = useFlash()
  const navigate = useNavigate()
  const submit = useSubmit()
  const inv = [keys.executions(dbId, hideRollback), keys.execution(item.id)]

  const enqueueAct = useAction<void, AsyncJobCreated>(() => api.post<AsyncJobCreated>(`/api/executions/${item.id}/enqueue`, {}), inv)
  const resumeAct = useAction<void, AsyncJobCreated>(() => api.post<AsyncJobCreated>(`/api/executions/${item.id}/resume`, {}), inv)
  const rollbackAct = useAction<void, ExecutionItem>(
    () => api.post<ExecutionItem>(`/api/databases/${dbId}/migrations/${item.migration_id}/rollback`, {}),
    inv,
  )
  const forwardAct = useAction<void, ExecutionItem>(
    () => api.post<ExecutionItem>(`/api/databases/${dbId}/migrations/${item.migration_id}/forward`, {}),
    inv,
  )

  const onEnqueue = async () => {
    await submit.run(async () => {
      const res = await enqueueAct.mutateAsync()
      flash.success(`Job #${res.job_id} masuk antrean`)
      navigate(`/executions/${item.id}?job_id=${res.job_id}`)
    })
  }

  const onResume = async () => {
    await submit.run(async () => {
      const res = await resumeAct.mutateAsync()
      flash.success(`Resume Job #${res.job_id} masuk antrean`)
      navigate(`/executions/${item.id}?job_id=${res.job_id}`)
    })
  }

  const onRollback = async () => {
    await submit.run(async () => {
      const res = await rollbackAct.mutateAsync()
      flash.success('Permintaan rollback diajukan untuk review leader')
      if (res?.id) {
        navigate(`/executions/${res.id}`)
      }
    })
  }

  const onForward = async () => {
    await submit.run(async () => {
      const res = await forwardAct.mutateAsync()
      flash.success('Permintaan forward diajukan untuk review leader')
      if (res?.id) {
        navigate(`/executions/${res.id}`)
      }
    })
  }

  const canRollback =
    canEdit &&
    isTopActive &&
    !isRolledBack &&
    (item.kind === 'apply' || item.kind === 'forward') &&
    item.status === 'success' &&
    !item.irreversible

  const canForward =
    canEdit &&
    ((item.status === 'failed' && (item.kind === 'apply' || item.kind === 'forward')) || isRolledBack)

  const canResume = canEdit && (item.status === 'partial' || item.status === 'partially_reverted')

  return (
    <tr>
      <td data-label="Exec ID">
        <Link to={`/executions/${item.id}`}>
          <strong>E{item.id}</strong>
        </Link>
      </td>
      <td data-label="Versi">{item.version_applied ? `v${item.version_applied}` : <span className="muted">—</span>}</td>
      <td data-label="Migration & Revisi">
        <div>
          <strong>{item.migration_title || `Migration #${item.migration_id}`}</strong>{' '}
          <span className="muted small">(rev #{item.revision_no ?? item.revision_id})</span>
        </div>
        {item.irreversible && (
          <div className="small muted" title={item.irreversible_reason || ''}>
            <Badge tone="warn">Irreversible</Badge> {item.irreversible_reason}
          </div>
        )}
      </td>
      <td data-label="Jenis">
        <Badge tone={item.kind === 'rollback' ? 'warn' : item.kind === 'forward' ? 'info' : 'neutral'}>
          {item.kind.toUpperCase()}
        </Badge>
        {item.rolls_back_execution_id && (
          <div className="small muted">
            ↺ membatalkan <Link to={`/executions/${item.rolls_back_execution_id}`}>E{item.rolls_back_execution_id}</Link>
          </div>
        )}
      </td>
      <td data-label="Status">
        <Badge tone={statusBadgeTone(item.status)}>{statusLabel(item.status)}</Badge>
        {item.error_message && <div className="field-msg small">{item.error_message}</div>}
      </td>
      <td data-label="Progress Batch">
        {item.batches_committed > 0 || item.exec_mode === 'batch' ? (
          <span className="small">
            {item.batches_committed} batch · {item.rows_committed.toLocaleString('id-ID')} baris
          </span>
        ) : (
          <span className="muted small">Mode normal</span>
        )}
      </td>
      <td data-label="Pengaju & Waktu">
        <div>{item.requested_by_username || `User #${item.requested_by}`}</div>
        <div className="muted small">{formatDateTime(item.created_at)}</div>
      </td>
      <td className="row-actions">
        <Link className="btn btn-ghost" to={`/executions/${item.id}`}>
          Detail
        </Link>
        {canEdit && item.status === 'approved' && (
          <button type="button" className="btn btn-primary" disabled={submit.pending} onClick={() => void onEnqueue()}>
            Jalankan
          </button>
        )}
        {canResume && (
          <button type="button" className="btn btn-primary" disabled={submit.pending} onClick={() => void onResume()}>
            {item.status === 'partially_reverted' ? 'Resume Revert' : 'Resume'}
          </button>
        )}
        {canRollback && (
          <ConfirmButton
            label="Rollback"
            confirmLabel="Ajukan Rollback"
            disabled={submit.pending}
            onConfirm={() => void onRollback()}
          />
        )}
        {canForward && (
          <button type="button" className="btn btn-ghost" disabled={submit.pending} onClick={() => void onForward()}>
            Forward
          </button>
        )}
        {submit.error && <Alert kind="error">{submit.error}</Alert>}
      </td>
    </tr>
  )
}
