import { describe, expect, it } from 'vitest'
import type { ExecutionItem, ExecutionStatus, MigrationItem } from '../../api/types'
import { executionsOf, migrationState, workflowStep } from './status'

function migration(over: Partial<MigrationItem> = {}, rev: Partial<MigrationItem['revision']> = {}): MigrationItem {
  return {
    id: 1,
    database_id: 1,
    title: 'Tambah kolom',
    version: null,
    created_by: 1,
    created_by_username: 'alice',
    created_at: '2026-11-25T00:00:00Z',
    ...over,
    revision: {
      id: 10,
      revision_no: 1,
      up_sql: 'SELECT 1',
      down_sql: 'SELECT 1',
      irreversible: false,
      irreversible_reason: null,
      exec_mode: 'normal',
      batch_table: null,
      batch_column: null,
      batch_size: null,
      sql_hash: 'h',
      frozen_at: null,
      ...rev,
    },
  }
}

function exec(id: number, status: ExecutionStatus, kind: ExecutionItem['kind'] = 'apply', migrationId = 1): ExecutionItem {
  return {
    id,
    database_id: 1,
    migration_id: migrationId,
    revision_id: 10,
    kind,
    status,
    requested_by: 1,
    approval_round: 1,
    batches_committed: 0,
    rows_committed: 0,
    created_at: '2026-11-25T00:00:00Z',
  }
}

const FROZEN = { frozen_at: '2026-11-25T00:00:00Z' }

describe('migrationState', () => {
  it('draft baru boleh diajukan dan dihapus', () => {
    const s = migrationState(migration(), [])
    expect(s).toMatchObject({ label: 'Draft', canSubmit: true, canDelete: true, open: false })
  })

  it('menunggu approval: tidak bisa diajukan ulang atau dihapus', () => {
    const s = migrationState(migration({}, FROZEN), [exec(5, 'pending_review')])
    expect(s).toMatchObject({ label: 'Menunggu approval', tone: 'warn', open: true, canSubmit: false, canDelete: false })
    expect(s.latest?.id).toBe(5)
  })

  it('aktif setelah sukses', () => {
    const s = migrationState(migration({ version: 3 }, FROZEN), [exec(5, 'success')])
    expect(s).toMatchObject({ label: 'Aktif · versi 3', tone: 'ok', canSubmit: false, canDelete: false })
  })

  it('execution terbuka lebih penting daripada status aktif (rollback sedang direview)', () => {
    const s = migrationState(migration({ version: 3 }, FROZEN), [exec(5, 'success'), exec(9, 'pending_review', 'rollback')])
    expect(s.label).toBe('Menunggu approval')
    expect(s.latest?.kind).toBe('rollback')
  })

  it('gagal lalu diubah: revisi baru kembali draft dan boleh diajukan, tapi tidak boleh dihapus', () => {
    const s = migrationState(migration({}, { revision_no: 2 }), [exec(5, 'failed')])
    expect(s).toMatchObject({ label: 'Draft · revisi 2', canSubmit: true, canDelete: false })
  })

  it('setelah rollback sukses', () => {
    const s = migrationState(migration({}, FROZEN), [exec(5, 'success'), exec(7, 'success', 'rollback')])
    expect(s).toMatchObject({ label: 'Di-rollback', canSubmit: false })
  })

  it('hanya memakai execution milik migration itu', () => {
    expect(executionsOf(1, [exec(1, 'failed'), exec(2, 'success', 'apply', 2), exec(3, 'rejected')]).map(e => e.id)).toEqual([3, 1])
  })
})

describe('workflowStep', () => {
  it('memetakan status ke langkah stepper', () => {
    expect(workflowStep(null, false)).toEqual({ index: 0, failed: false })
    expect(workflowStep(exec(1, 'pending_review'), true)).toEqual({ index: 1, failed: false })
    expect(workflowStep(exec(1, 'rejected'), true)).toEqual({ index: 1, failed: true })
    expect(workflowStep(exec(1, 'approved'), true)).toEqual({ index: 2, failed: false })
    expect(workflowStep(exec(1, 'running'), true)).toEqual({ index: 4, failed: false })
    expect(workflowStep(exec(1, 'success'), true)).toEqual({ index: 5, failed: false })
    expect(workflowStep(exec(1, 'failed'), true)).toEqual({ index: 5, failed: true })
  })

  it('revisi baru setelah gagal kembali ke langkah draft', () => {
    expect(workflowStep(exec(1, 'failed'), false)).toEqual({ index: 0, failed: false })
  })
})
