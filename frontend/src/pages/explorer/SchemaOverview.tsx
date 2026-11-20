import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useDatabaseSchema } from '../../api/hooks'
import { Badge, PageHeader, QueryState, TableWrap } from '../../components/ui'
import { MigrationExplainRunner } from './ExplainView'

/**
 * Halaman Overview Skema & Katalog Tabel (/db/:dbID) — Paket B4.
 * Menampilkan daftar tabel dan view di database target, pencarian cepat, tautan ke struktur/data,
 * tautan ke riwayat eksekusi & migration, serta panel analisis EXPLAIN bagi pemilik akses edit.
 */
export function SchemaOverviewPage() {
  const { dbID } = useParams<{ dbID: string }>()
  const id = Number(dbID)
  const query = useDatabaseSchema(id)
  const [search, setSearch] = useState('')
  const [showExplain, setShowExplain] = useState(false)

  return (
    <QueryState query={query}>
      {({ database, tables }) => {
        const q = search.trim().toLowerCase()
        const filtered = q
          ? tables.filter(t => `${t.schema}.${t.name}`.toLowerCase().includes(q) || t.type.toLowerCase().includes(q))
          : tables
        const canEdit = database.level === 'edit'

        return (
          <>
            <div className="breadcrumb small muted">
              <Link to="/">Dashboard</Link> <span>/</span> <strong>{database.name}</strong>
            </div>

            <PageHeader
              title={`${database.name} · Katalog Skema`}
              actions={
                <div className="form-actions">
                  <Badge tone={canEdit ? 'ok' : 'info'}>{canEdit ? 'view + edit' : 'view only'}</Badge>
                  <Link className="btn btn-ghost" to={`/db/${id}/executions`}>
                    Riwayat Eksekusi
                  </Link>
                  <a className="btn btn-ghost" href={`/db/${id}/migrations`}>
                    Migrations
                  </a>
                  {canEdit && (
                    <button type="button" className="btn btn-primary" onClick={() => setShowExplain(v => !v)}>
                      {showExplain ? 'Tutup EXPLAIN' : 'Analisis EXPLAIN'}
                    </button>
                  )}
                </div>
              }
            />

            {database.host && (
              <p className="muted small">
                Target: <code>{database.host}:{database.port}/{database.db_name}</code> · Total objek: {tables.length}
              </p>
            )}

            {canEdit && showExplain && <MigrationExplainRunner dbId={id} />}

            <div className="form-row">
              <input
                type="search"
                placeholder="Cari nama skema atau tabel…"
                aria-label="Cari nama skema atau tabel"
                value={search}
                onChange={e => setSearch(e.target.value)}
              />
            </div>

            {filtered.length === 0 ? (
              <div className="card empty">
                <p>
                  {tables.length === 0
                    ? 'Database target ini belum memiliki tabel atau view.'
                    : 'Tidak ada tabel yang cocok dengan pencarian.'}
                </p>
              </div>
            ) : (
              <TableWrap>
                <table>
                  <thead>
                    <tr>
                      <th>Skema</th>
                      <th>Nama Tabel / View</th>
                      <th>Tipe</th>
                      <th>Estimasi Baris</th>
                      <th>
                        <span className="sr-only">Aksi</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map(t => {
                      const base = `/db/${id}/tables/${encodeURIComponent(t.schema)}/${encodeURIComponent(t.name)}`
                      return (
                        <tr key={`${t.schema}.${t.name}`}>
                          <td data-label="Skema">
                            <code>{t.schema}</code>
                          </td>
                          <td data-label="Nama Tabel / View">
                            <strong>{t.name}</strong>
                          </td>
                          <td data-label="Tipe">
                            <Badge tone={t.type === 'VIEW' ? 'info' : 'neutral'}>{t.type}</Badge>
                          </td>
                          <td data-label="Estimasi Baris">{t.approx_rows.toLocaleString('id-ID')}</td>
                          <td className="row-actions">
                            <Link className="btn btn-ghost" to={base}>
                              Struktur
                            </Link>
                            <Link className="btn btn-primary" to={`${base}/data`}>
                              Lihat Data
                            </Link>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </TableWrap>
            )}
          </>
        )
      }}
    </QueryState>
  )
}
