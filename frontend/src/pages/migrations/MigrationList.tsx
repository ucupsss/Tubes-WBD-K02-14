import { Link, useParams } from 'react-router-dom'
import { useAccessibleDatabases, useMigrations, useRecentExecutions } from '../../api/hooks'
import type { AccessibleDatabase, ExecutionItem } from '../../api/types'
import { Alert, Badge, PageHeader, QueryState, TableWrap } from '../../components/ui'
import { formatDateTime } from '../../lib/form'
import { migrationState } from './status'

/** Database yang sedang dibuka beserta level akses pengguna (view/edit). */
export function useCurrentDatabase(dbId: number): AccessibleDatabase | undefined {
  const dbs = useAccessibleDatabases()
  return dbs.data?.items.find(d => d.id === dbId)
}

export function Breadcrumb({ dbId, dbName, children }: { dbId: number; dbName?: string; children?: React.ReactNode }) {
  return (
    <nav className="breadcrumb muted small" aria-label="Breadcrumb">
      <Link to="/">Dashboard</Link> <span>/</span> <Link to={`/db/${dbId}`}>{dbName ?? `Database #${dbId}`}</Link>{' '}
      <span>/</span> {children ?? <strong>Migration</strong>}
    </nav>
  )
}

/** Halaman daftar migration satu database (/db/:dbID/migrations) — paket A6. */
export function MigrationListPage() {
  const dbId = Number(useParams().dbID)
  const db = useCurrentDatabase(dbId)
  const migrations = useMigrations(dbId)
  const executions = useRecentExecutions(dbId)
  const canEdit = db?.level === 'edit'
  const execItems: ExecutionItem[] = executions.data?.items ?? []

  return (
    <>
      <Breadcrumb dbId={dbId} dbName={db?.name} />
      <PageHeader
        title="Migration"
        actions={
          <>
            <Link className="btn btn-ghost" to={`/db/${dbId}/executions`}>
              Riwayat eksekusi
            </Link>
            {canEdit && (
              <Link className="btn btn-primary" to={`/db/${dbId}/migrations/new`}>
                Migration baru
              </Link>
            )}
          </>
        }
      />
      {!canEdit && db && (
        <Alert kind="info">Anda hanya punya akses lihat ke database ini, jadi tidak dapat membuat atau mengajukan migration.</Alert>
      )}
      {executions.isError && <Alert>Status eksekusi gagal dimuat; kolom status mungkin belum lengkap.</Alert>}
      <QueryState query={migrations}>
        {({ items, total }) =>
          items.length === 0 ? (
            <div className="card empty">
              <p>Belum ada migration untuk database ini.</p>
              {canEdit && (
                <Link className="btn btn-primary" to={`/db/${dbId}/migrations/new`}>
                  Buat migration pertama
                </Link>
              )}
            </div>
          ) : (
            <>
              <TableWrap>
                <table>
                  <thead>
                    <tr>
                      <th>Versi</th>
                      <th>Judul</th>
                      <th>Status</th>
                      <th>Mode</th>
                      <th>Revisi</th>
                      <th>Dibuat</th>
                      <th>
                        <span className="sr-only">Aksi</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map(m => {
                      const st = migrationState(m, execItems)
                      return (
                        <tr key={m.id}>
                          <td data-label="Versi">{m.version ?? '—'}</td>
                          <td data-label="Judul">
                            <Link to={`/db/${dbId}/migrations/${m.id}`}>
                              <strong>{m.title}</strong>
                            </Link>
                            <div className="muted small">#{m.id}</div>
                          </td>
                          <td data-label="Status">
                            <Badge tone={st.tone}>{st.label}</Badge>
                          </td>
                          <td data-label="Mode">
                            {m.revision.exec_mode === 'batch' ? 'Batch' : 'Normal'}
                            {m.revision.irreversible && (
                              <>
                                {' '}
                                <Badge tone="warn">Irreversible</Badge>
                              </>
                            )}
                          </td>
                          <td data-label="Revisi">{m.revision.revision_no}</td>
                          <td data-label="Dibuat">
                            {m.created_by_username}
                            <div className="muted small">{formatDateTime(m.created_at)}</div>
                          </td>
                          <td className="row-actions">
                            <Link className="btn btn-ghost" to={`/db/${dbId}/migrations/${m.id}`}>
                              Detail
                            </Link>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </TableWrap>
              {total > items.length && (
                <p className="muted small">
                  Menampilkan {items.length} dari {total} migration.
                </p>
              )}
            </>
          )
        }
      </QueryState>
    </>
  )
}
