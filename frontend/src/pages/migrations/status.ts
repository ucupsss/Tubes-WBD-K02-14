import type { ExecutionItem, ExecutionStatus, MigrationItem } from '../../api/types'

type Tone = 'neutral' | 'ok' | 'warn' | 'danger' | 'info'

/** Status execution yang masih "terbuka": migration tidak bisa diajukan/diubah selama ini ada. */
export const OPEN_STATUSES: ExecutionStatus[] = [
  'pending_review',
  'approved',
  'queued',
  'running',
  'partial',
  'partially_reverted',
  'uncertain',
]

export interface MigrationState {
  label: string
  tone: Tone
  /** Execution terbaru milik migration ini (riwayat diurutkan id menurun oleh server). */
  latest: ExecutionItem | null
  /** Ada execution yang belum selesai: tombol ajukan/ubah/hapus disembunyikan. */
  open: boolean
  frozen: boolean
  /** Boleh diajukan (Submit for review): belum aktif, revisi belum dibekukan, tidak ada yang terbuka. */
  canSubmit: boolean
  /** Boleh dihapus: draft yang belum pernah diajukan. */
  canDelete: boolean
}

/** Execution milik satu migration, terbaru dulu. */
export function executionsOf(migrationId: number, executions: ExecutionItem[]): ExecutionItem[] {
  return executions.filter(e => e.migration_id === migrationId).sort((a, b) => b.id - a.id)
}

/**
 * Menurunkan status tampilan satu migration dari datanya dan riwayat execution-nya.
 * Aturan sebenarnya tetap di server; ini hanya supaya UI tidak menawarkan aksi yang pasti ditolak.
 */
export function migrationState(m: MigrationItem, executions: ExecutionItem[]): MigrationState {
  const mine = executionsOf(m.id, executions)
  const latest = mine[0] ?? null
  const open = mine.some(e => OPEN_STATUSES.includes(e.status))
  const frozen = m.revision.frozen_at !== null
  const active = m.version !== null

  let label: string
  let tone: Tone
  if (open && latest) {
    ;({ label, tone } = WORKFLOW_LABEL[latest.status] ?? { label: latest.status, tone: 'warn' })
  } else if (active) {
    label = `Aktif · versi ${m.version}`
    tone = 'ok'
  } else if (!frozen) {
    label = m.revision.revision_no > 1 ? `Draft · revisi ${m.revision.revision_no}` : 'Draft'
    tone = 'neutral'
  } else if (latest) {
    ;({ label, tone } = WORKFLOW_LABEL[latest.status] ?? { label: latest.status, tone: 'neutral' })
    if (latest.kind === 'rollback' && latest.status === 'success') {
      label = 'Di-rollback'
      tone = 'neutral'
    }
  } else {
    label = 'Diajukan'
    tone = 'warn'
  }

  return {
    label,
    tone,
    latest,
    open,
    frozen,
    canSubmit: !active && !frozen && !open,
    canDelete: !frozen && !open && mine.length === 0,
  }
}

const WORKFLOW_LABEL: Partial<Record<ExecutionStatus, { label: string; tone: Tone }>> = {
  pending_review: { label: 'Menunggu approval', tone: 'warn' },
  approved: { label: 'Disetujui · siap dijalankan', tone: 'info' },
  queued: { label: 'Dalam antrean', tone: 'info' },
  running: { label: 'Berjalan', tone: 'info' },
  success: { label: 'Sukses', tone: 'ok' },
  failed: { label: 'Gagal', tone: 'danger' },
  partial: { label: 'Sebagian (batch)', tone: 'warn' },
  partially_reverted: { label: 'Sebagian di-revert', tone: 'warn' },
  uncertain: { label: 'Hasil belum pasti', tone: 'warn' },
  rejected: { label: 'Ditolak', tone: 'danger' },
  cancelled: { label: 'Dibatalkan', tone: 'neutral' },
}

/** Langkah alur kerja untuk stepper di halaman detail. */
export const WORKFLOW_STEPS = ['Draft', 'Review leader', 'Disetujui', 'Antrean', 'Berjalan', 'Selesai'] as const

/**
 * Indeks langkah yang sedang berlangsung dan apakah alurnya berakhir gagal.
 * Draft (belum ada execution) = 0; status akhir (sukses/gagal/ditolak) = langkah terakhir.
 */
export function workflowStep(latest: ExecutionItem | null, frozen: boolean): { index: number; failed: boolean } {
  if (!latest || !frozen) {
    return { index: 0, failed: false }
  }
  switch (latest.status) {
    case 'pending_review':
      return { index: 1, failed: false }
    case 'rejected':
      return { index: 1, failed: true }
    case 'approved':
      return { index: 2, failed: false }
    case 'queued':
      return { index: 3, failed: false }
    case 'running':
    case 'uncertain':
      return { index: 4, failed: false }
    case 'success':
      return { index: 5, failed: false }
    case 'cancelled':
      return { index: 3, failed: true }
    default:
      // failed, partial, partially_reverted
      return { index: 5, failed: true }
  }
}
