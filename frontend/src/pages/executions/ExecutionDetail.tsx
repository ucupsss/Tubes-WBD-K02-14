import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, useExecutionDetail, useJobStatus } from '../../api/hooks'
import type { AsyncJobCreated, ExecutionApprovalItem, ExecutionAttemptItem, ExecutionBatchItem, ExecutionItem } from '../../api/types'
import { Alert, Badge, ConfirmButton, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useAuth } from '../../lib/auth'
import { formatDateTime, useSubmit } from '../../lib/form'
import { statusBadgeTone, statusLabel } from './ExecutionHistory'

/**
 * Halaman Detail Execution & Monitor Job (/executions/:execID) — Paket B5.
 * Menampilkan:
 * 1. Ringkasan Execution (status, jenis, versi, rujukan rollback, alasan irreversible).
 * 2. Live Job Monitor (polling otomatis saat queued/running/uncertain, jumlah batch & baris committed).
 * 3. Tombol aksi: Approve/Reject (bagi leader), Jalankan (Enqueue), Resume, Revert/Rollback, Forward, Cancel.
 * 4. Tabel Persetujuan Leader per Group.
 * 5. Tabel Checkpoint Batch (Apply & Revert).
 * 6. Tabel Riwayat Attempt (beserta salinan SQL yang dijalankan, SQLSTATE, dan trace_id).
 */
export function ExecutionDetailPage() {
  const { execID } = useParams<{ execID: string }>()
  const [searchParams, setSearchParams] = useSearchParams()
  const qc = useQueryClient()
  const id = Number(execID)
  const initialJobId = Number(searchParams.get('job_id') || '0') || null
  const [activeJobId, setActiveJobId] = useState<number | null>(initialJobId)

  useEffect(() => {
    setActiveJobId(Number(searchParams.get('job_id') || '0') || null)
  }, [execID, searchParams])

  // Polling otomatis setiap 1,5 detik hanya selama execution sedang queued/running/uncertain.
  const detailQuery = useExecutionDetail(id, 1500)
  const currentJobId = activeJobId ?? detailQuery.data?.job_id ?? null
  const jobQuery = useJobStatus(currentJobId, 1500)

  const jobStatus = jobQuery.data?.status
  useEffect(() => {
    if (jobStatus === 'done' || jobStatus === 'dead') {
      void qc.invalidateQueries({ queryKey: keys.execution(id) })
    }
  }, [jobStatus, id, qc])

  return (
    <QueryState query={detailQuery}>
      {data => {
        const exec = data.execution
        const isLive = ['queued', 'running', 'uncertain'].includes(exec.status)
        const batchesCommitted = jobQuery.data?.batches_committed ?? exec.batches_committed
        const rowsCommitted = jobQuery.data?.rows_committed ?? exec.rows_committed

        return (
          <>
            <div className="breadcrumb small muted">
              <Link to="/">Dashboard</Link> <span>/</span>{' '}
              <Link to={`/db/${exec.database_id}`}>Database #{exec.database_id}</Link> <span>/</span>{' '}
              <Link to={`/db/${exec.database_id}/executions`}>Riwayat Eksekusi</Link> <span>/</span>{' '}
              <strong>Execution #{exec.id}</strong>
            </div>

            <PageHeader
              title={`Execution #${exec.id} · ${exec.migration_title || `Migration #${exec.migration_id}`}`}
              actions={
                <div className="form-actions">
                  <Badge tone={statusBadgeTone(exec.status)}>{statusLabel(exec.status)}</Badge>
                  <Link className="btn btn-ghost" to={`/db/${exec.database_id}/executions`}>
                    ← Kembali ke Riwayat
                  </Link>
                </div>
              }
            />

            {/* Monitor Job Asinkron (B5) */}
            {(isLive || currentJobId || exec.status === 'partial' || exec.status === 'partially_reverted') && (
              <div className="card job-monitor">
                <div className="page-header">
                  <h2>Monitor Eksekusi & Job Asinkron</h2>
                  <div className="form-actions">
                    {currentJobId && <Badge tone="info">Job #{currentJobId}</Badge>}
                    {isLive && <Badge tone="info">Memantau otomatis (polling)…</Badge>}
                  </div>
                </div>
                <div className="grid-2">
                  <div>
                    <div className="muted small">Status Execution</div>
                    <strong>{statusLabel(exec.status)}</strong>
                    {jobQuery.data?.queue_wait_ms !== undefined && (
                      <div className="muted small">Waktu tunggu antrean: {jobQuery.data.queue_wait_ms} ms</div>
                    )}
                  </div>
                  <div>
                    <div className="muted small">Progress Commit di Target DB</div>
                    <strong>
                      {batchesCommitted} batch · {rowsCommitted.toLocaleString('id-ID')} baris committed
                    </strong>
                    {(exec.status === 'partial' || exec.status === 'partially_reverted') && (
                      <div className="small muted">
                        Sebagian batch sudah tersimpan aman di checkpoint <code>_dbmp.batch_checkpoints</code>. Anda
                        dapat menekan <strong>Resume</strong> untuk melanjutkan dari batch terakhir tanpa mengulang
                        batch yang sudah selesai, atau <strong>Revert</strong> untuk membatalkan batch yang sudah
                        committed dalam urutan terbalik.
                      </div>
                    )}
                  </div>
                </div>
              </div>
            )}

            {/* Kartu Ringkasan & Tombol Aksi */}
            <ExecutionSummaryCard
              exec={exec}
              approvals={data.approvals || []}
              canEdit={data.can_edit ?? true}
              onJobCreated={jid => {
                setActiveJobId(jid)
                setSearchParams({ job_id: String(jid) })
              }}
            />

            {/* Salinan SQL Up / Down */}
            {(exec.up_sql || exec.down_sql) && (
              <div className="card">
                <h2>Definisi SQL Revisi #{exec.revision_no ?? exec.revision_id}</h2>
                {exec.up_sql && (
                  <>
                    <h3>SQL Up</h3>
                    <pre className="sql-box">{exec.up_sql}</pre>
                  </>
                )}
                {exec.irreversible ? (
                  <Alert kind="info">
                    <strong>Migration Irreversible:</strong> {exec.irreversible_reason || 'Tidak memiliki SQL Down'}
                  </Alert>
                ) : (
                  exec.down_sql && (
                    <>
                      <h3>SQL Down</h3>
                      <pre className="sql-box">{exec.down_sql}</pre>
                    </>
                  )
                )}
              </div>
            )}

            {/* Persetujuan Group Leader */}
            <h2>Persetujuan Group Leader (Ronde #{exec.approval_round})</h2>
            <ApprovalsTable approvals={data.approvals || []} />

            {/* Checkpoint Batch */}
            {(data.batches?.length > 0 || exec.exec_mode === 'batch' || exec.batches_committed > 0) && (
              <>
                <h2>Checkpoint Batch ({data.batches?.length ?? 0})</h2>
                <BatchesTable batches={data.batches || []} />
              </>
            )}

            {/* Daftar Attempt */}
            <h2>Riwayat Percobaan / Attempts ({data.attempts?.length ?? 0})</h2>
            <AttemptsTable attempts={data.attempts || []} />
          </>
        )
      }}
    </QueryState>
  )
}

function ExecutionSummaryCard({
  exec,
  approvals,
  canEdit,
  onJobCreated,
}: {
  exec: ExecutionItem
  approvals: ExecutionApprovalItem[]
  canEdit: boolean
  onJobCreated: (jobId: number) => void
}) {
  const { me } = useAuth()
  const flash = useFlash()
  const navigate = useNavigate()
  const submit = useSubmit()
  const [comment, setComment] = useState('')

  const inv = [
    keys.execution(exec.id),
    keys.executionsAll(exec.database_id),
    keys.migrations(exec.database_id),
    keys.approvals,
  ]

  const approveAct = useAction<void>(() => api.post(`/api/executions/${exec.id}/approve`, { comment }), inv)
  const rejectAct = useAction<void>(() => api.post(`/api/executions/${exec.id}/reject`, { comment }), inv)
  const enqueueAct = useAction<void, AsyncJobCreated>(() => api.post<AsyncJobCreated>(`/api/executions/${exec.id}/enqueue`, {}), inv)
  const resumeAct = useAction<void, AsyncJobCreated>(() => api.post<AsyncJobCreated>(`/api/executions/${exec.id}/resume`, {}), inv)
  const cancelAct = useAction<void>(() => api.post(`/api/executions/${exec.id}/cancel`, {}), inv)
  const rollbackAct = useAction<void, ExecutionItem>(
    () => api.post<ExecutionItem>(`/api/databases/${exec.database_id}/migrations/${exec.migration_id}/rollback`, {}),
    inv,
  )
  const forwardAct = useAction<void, ExecutionItem>(
    () => api.post<ExecutionItem>(`/api/databases/${exec.database_id}/migrations/${exec.migration_id}/forward`, {}),
    inv,
  )

  const ledGroupIds = new Set((me?.led_groups || []).map(g => g.id))
  const hasPendingForMe =
    exec.status === 'pending_review' &&
    approvals.some(a => a.decision === 'pending' && (a.leader_id === me?.user.id || ledGroupIds.has(a.group_id)))

  const handleEnqueue = async () => {
    await submit.run(async () => {
      const res = await enqueueAct.mutateAsync()
      flash.success(`Execution #${exec.id} masuk antrean (Job #${res.job_id})`)
      onJobCreated(res.job_id)
    })
  }

  const handleResume = async () => {
    await submit.run(async () => {
      const res = await resumeAct.mutateAsync()
      flash.success(`Resume Execution #${exec.id} masuk antrean (Job #${res.job_id})`)
      onJobCreated(res.job_id)
    })
  }

  const handleRollback = async () => {
    await submit.run(async () => {
      const res = await rollbackAct.mutateAsync()
      flash.success('Execution rollback berhasil dibuat dan menunggu approval leader')
      if (res?.id) {
        navigate(`/executions/${res.id}`)
      }
    })
  }

  const handleForward = async () => {
    await submit.run(async () => {
      const res = await forwardAct.mutateAsync()
      flash.success('Execution forward berhasil dibuat dan menunggu approval leader')
      if (res?.id) {
        navigate(`/executions/${res.id}`)
      }
    })
  }

  const handleCancel = async () => {
    const ok = await submit.run(() => cancelAct.mutateAsync())
    if (ok) {
      flash.success('Execution dibatalkan')
    }
  }

  return (
    <div className="card">
      <div className="grid-2">
        <div>
          <p>
            <strong>ID Execution:</strong> E{exec.id}
          </p>
          <p>
            <strong>Jenis Eksekusi:</strong> <Badge tone="info">{exec.kind.toUpperCase()}</Badge>
            {exec.rolls_back_execution_id && (
              <span>
                {' '}
                (Membatalkan{' '}
                <Link to={`/executions/${exec.rolls_back_execution_id}`}>Execution #{exec.rolls_back_execution_id}</Link>)
              </span>
            )}
          </p>
          <p>
            <strong>Versi Rantai Aktif:</strong> {exec.version_applied ? `v${exec.version_applied}` : '—'}
          </p>
          <p>
            <strong>Diajukan oleh:</strong> {exec.requested_by_username || `User #${exec.requested_by}`} pada{' '}
            {formatDateTime(exec.created_at)}
          </p>
        </div>
        <div>
          <p>
            <strong>Mulai Eksekusi:</strong> {exec.started_at ? formatDateTime(exec.started_at) : '—'}
          </p>
          <p>
            <strong>Selesai Eksekusi:</strong> {exec.finished_at ? formatDateTime(exec.finished_at) : '—'}
          </p>
          <p>
            <strong>Progress Batch:</strong> {exec.batches_committed} batch ·{' '}
            {exec.rows_committed.toLocaleString('id-ID')} baris
          </p>
          {exec.irreversible && (
            <p>
              <Badge tone="warn">Irreversible</Badge> {exec.irreversible_reason}
            </p>
          )}
        </div>
      </div>

      {exec.error_message && (
        <Alert kind="error">
          <strong>Gagal{exec.error_code ? ` (${exec.error_code})` : ''}:</strong> {exec.error_message}
        </Alert>
      )}

      {hasPendingForMe && (
        <div className="form-row">
          <input
            type="text"
            placeholder="Catatan review / alasan keputusan (opsional)…"
            value={comment}
            onChange={e => setComment(e.target.value)}
          />
          <button
            type="button"
            className="btn btn-primary"
            disabled={submit.pending}
            onClick={() =>
              void submit.run(async () => {
                await approveAct.mutateAsync()
                flash.success('Approval berhasil dicatat')
              })
            }
          >
            Setujui (Approve)
          </button>
          <button
            type="button"
            className="btn btn-danger-outline"
            disabled={submit.pending}
            onClick={() =>
              void submit.run(async () => {
                await rejectAct.mutateAsync()
                flash.success('Execution ditolak (Rejected)')
              })
            }
          >
            Tolak (Reject)
          </button>
        </div>
      )}

      {canEdit && (
        <div className="form-actions" style={{ marginTop: 12 }}>
          {exec.status === 'approved' && (
            <button type="button" className="btn btn-primary" disabled={submit.pending} onClick={() => void handleEnqueue()}>
              Jalankan (Enqueue ke Worker)
            </button>
          )}

          {((exec.status === 'partial' && !exec.has_active_revert) || exec.status === 'partially_reverted') && (
            <button type="button" className="btn btn-primary" disabled={submit.pending} onClick={() => void handleResume()}>
              {exec.status === 'partially_reverted'
                ? 'Resume Revert dari Checkpoint Terakhir'
                : 'Resume Batch dari Checkpoint Terakhir'}
            </button>
          )}

          {exec.status === 'partial' && !exec.has_active_revert && !exec.irreversible && (
            <ConfirmButton
              label="Revert Batch yang Sudah Committed"
              confirmLabel="Ya, Ajukan Revert"
              disabled={submit.pending}
              onConfirm={() => void handleRollback()}
            />
          )}

          {exec.status === 'success' &&
            (exec.kind === 'apply' || exec.kind === 'forward') &&
            exec.is_top_of_chain &&
            !exec.is_rolled_back &&
            !exec.irreversible && (
              <ConfirmButton
                label="Rollback Migration Ini"
                confirmLabel="Ya, Ajukan Rollback"
                disabled={submit.pending}
                onConfirm={() => void handleRollback()}
              />
            )}

          {((exec.status === 'failed' && (exec.kind === 'apply' || exec.kind === 'forward')) ||
            (exec.kind === 'rollback' && exec.status === 'success')) && (
            <button type="button" className="btn btn-ghost" disabled={submit.pending} onClick={() => void handleForward()}>
              Forward (Jalankan Ulang Up)
            </button>
          )}

          {(exec.status === 'approved' || exec.status === 'queued') && exec.batches_committed === 0 && (
            <ConfirmButton
              label="Batalkan Permintaan"
              confirmLabel="Ya, Batalkan"
              disabled={submit.pending}
              onConfirm={() => void handleCancel()}
            />
          )}
        </div>
      )}

      {submit.error && <Alert kind="error">{submit.error}</Alert>}
    </div>
  )
}

function ApprovalsTable({ approvals }: { approvals: ExecutionApprovalItem[] }) {
  if (approvals.length === 0) {
    return (
      <div className="card empty">
        <p>Belum ada data baris approval untuk ronde ini.</p>
      </div>
    )
  }
  return (
    <TableWrap>
      <table>
        <thead>
          <tr>
            <th>Group</th>
            <th>Leader Group</th>
            <th>Keputusan</th>
            <th>Catatan</th>
            <th>Waktu Keputusan</th>
          </tr>
        </thead>
        <tbody>
          {approvals.map(a => (
            <tr key={`${a.round}-${a.group_id}`}>
              <td data-label="Group">
                <strong>{a.group_name}</strong>
              </td>
              <td data-label="Leader Group">
                {a.leader_name ? (
                  a.leader_name
                ) : (
                  <Badge tone="warn">Menunggu penetapan leader group {a.group_name}</Badge>
                )}
              </td>
              <td data-label="Keputusan">
                <Badge
                  tone={a.decision === 'approved' ? 'ok' : a.decision === 'rejected' ? 'danger' : 'warn'}
                >
                  {a.decision.toUpperCase()}
                </Badge>
              </td>
              <td data-label="Catatan">{a.comment || <span className="muted">—</span>}</td>
              <td data-label="Waktu Keputusan">
                {a.decided_at ? (
                  <span>
                    {formatDateTime(a.decided_at)} {a.decided_by_name ? `oleh ${a.decided_by_name}` : ''}
                  </span>
                ) : (
                  <span className="muted">—</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableWrap>
  )
}

function BatchesTable({ batches }: { batches: ExecutionBatchItem[] }) {
  if (batches.length === 0) {
    return (
      <div className="card empty">
        <p>Belum ada batch yang di-commit untuk execution ini.</p>
      </div>
    )
  }
  return (
    <TableWrap>
      <table>
        <thead>
          <tr>
            <th>Arah</th>
            <th>Batch #</th>
            <th>Rentang Cursor (:from .. :to]</th>
            <th>Baris Terdampak</th>
            <th>Waktu Commit</th>
          </tr>
        </thead>
        <tbody>
          {batches.map(b => (
            <tr key={`${b.direction}-${b.batch_no}`}>
              <td data-label="Arah">
                <Badge tone={b.direction === 'revert' ? 'warn' : 'ok'}>{b.direction.toUpperCase()}</Badge>
              </td>
              <td data-label="Batch #">#{b.batch_no}</td>
              <td data-label="Rentang Cursor">
                <code>
                  ({b.from_value} .. {b.to_value}]
                </code>
              </td>
              <td data-label="Baris Terdampak">{b.rows_affected.toLocaleString('id-ID')}</td>
              <td data-label="Waktu Commit">{formatDateTime(b.committed_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </TableWrap>
  )
}

function AttemptsTable({ attempts }: { attempts: ExecutionAttemptItem[] }) {
  if (attempts.length === 0) {
    return (
      <div className="card empty">
        <p>Belum ada attempt eksekusi oleh worker.</p>
      </div>
    )
  }
  return (
    <div className="attempts-list">
      {attempts.map(att => (
        <div key={att.id || att.attempt_no} className="card">
          <div className="page-header">
            <h3>
              Attempt #{att.attempt_no}{' '}
              <Badge
                tone={
                  att.status === 'success'
                    ? 'ok'
                    : att.status === 'failed'
                      ? 'danger'
                      : att.status === 'uncertain'
                        ? 'warn'
                        : 'info'
                }
              >
                {att.status.toUpperCase()}
              </Badge>
            </h3>
            <div className="muted small">
              {att.worker_id && <span>Worker: <code>{att.worker_id}</code> · </span>}
              {att.trace_id && <span>Trace ID: <code>{att.trace_id}</code> · </span>}
              Mulai: {formatDateTime(att.started_at)}
              {att.finished_at && ` · Selesai: ${formatDateTime(att.finished_at)}`}
            </div>
          </div>

          {att.error_message && (
            <Alert kind="error">
              <strong>Error{att.sqlstate ? ` (SQLSTATE ${att.sqlstate})` : ''}:</strong> {att.error_message}
            </Alert>
          )}

          <div className="muted small" style={{ marginBottom: 4 }}>
            Salinan SQL yang dieksekusi:
          </div>
          <pre className="sql-box">{att.sql_executed}</pre>
        </div>
      ))}
    </div>
  )
}
