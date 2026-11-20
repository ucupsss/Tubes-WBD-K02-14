import { Link, useParams } from 'react-router-dom'
import { useTableMetadata } from '../../api/hooks'
import { Badge, PageHeader, QueryState, TableWrap } from '../../components/ui'

/**
 * Halaman Detail Struktur Tabel (/db/:dbID/tables/:schema/:table) — Paket B4.
 * Menampilkan kolom (tipe data, nullable, default, PK), constraint (PK, FK, UNIQUE, CHECK), dan indeks.
 */
export function TableDetailPage() {
  const { dbID, schema = '', table = '' } = useParams<{ dbID: string; schema: string; table: string }>()
  const id = Number(dbID)
  const query = useTableMetadata(id, schema, table)

  return (
    <QueryState query={query}>
      {({ database, metadata }) => (
        <>
          <div className="breadcrumb small muted">
            <Link to="/">Dashboard</Link> <span>/</span> <Link to={`/db/${id}`}>{database.name}</Link> <span>/</span>{' '}
            <strong>
              {metadata.schema}.{metadata.table}
            </strong>
          </div>

          <PageHeader
            title={`${metadata.schema}.${metadata.table} · Struktur Tabel`}
            actions={
              <div className="form-actions">
                <Link className="btn btn-ghost" to={`/db/${id}`}>
                  Kembali ke Skema
                </Link>
                <Link
                  className="btn btn-primary"
                  to={`/db/${id}/tables/${encodeURIComponent(metadata.schema)}/${encodeURIComponent(metadata.table)}/data`}
                >
                  Lihat Data Baris
                </Link>
              </div>
            }
          />

          <h2>Kolom ({metadata.columns.length})</h2>
          <TableWrap>
            <table>
              <thead>
                <tr>
                  <th>Nama Kolom</th>
                  <th>Tipe Data</th>
                  <th>Nullable</th>
                  <th>Default</th>
                  <th>Keterangan</th>
                </tr>
              </thead>
              <tbody>
                {metadata.columns.map(col => (
                  <tr key={col.name}>
                    <td data-label="Nama Kolom">
                      <strong>{col.name}</strong>
                    </td>
                    <td data-label="Tipe Data">
                      <code>{col.data_type}</code>
                    </td>
                    <td data-label="Nullable">{col.is_nullable ? 'YES' : 'NO'}</td>
                    <td data-label="Default">
                      {col.default_value ? <code>{col.default_value}</code> : <span className="muted">—</span>}
                    </td>
                    <td data-label="Keterangan">
                      {col.is_primary_key ? <Badge tone="ok">PRIMARY KEY</Badge> : <span className="muted">—</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableWrap>

          <h2>Constraints ({metadata.constraints.length})</h2>
          {metadata.constraints.length === 0 ? (
            <div className="card empty">
              <p>Tidak ada constraint pada tabel ini.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Nama Constraint</th>
                    <th>Tipe</th>
                    <th>Definisi</th>
                    <th>Referensi FK</th>
                  </tr>
                </thead>
                <tbody>
                  {metadata.constraints.map(c => (
                    <tr key={c.name}>
                      <td data-label="Nama Constraint">
                        <code>{c.name}</code>
                      </td>
                      <td data-label="Tipe">
                        <Badge tone={c.type === 'PRIMARY KEY' ? 'ok' : c.type === 'FOREIGN KEY' ? 'info' : 'neutral'}>
                          {c.type}
                        </Badge>
                      </td>
                      <td data-label="Definisi">
                        <code>{c.definition}</code>
                      </td>
                      <td data-label="Referensi FK">
                        {c.foreign_table ? (
                          <code>
                            {c.foreign_table}({c.foreign_column})
                          </code>
                        ) : (
                          <span className="muted">—</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableWrap>
          )}

          <h2>Indexes ({metadata.indexes.length})</h2>
          {metadata.indexes.length === 0 ? (
            <div className="card empty">
              <p>Tidak ada indeks pada tabel ini.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Nama Indeks</th>
                    <th>Properti</th>
                    <th>Definisi SQL</th>
                  </tr>
                </thead>
                <tbody>
                  {metadata.indexes.map(idx => (
                    <tr key={idx.name}>
                      <td data-label="Nama Indeks">
                        <code>{idx.name}</code>
                      </td>
                      <td data-label="Properti">
                        {idx.is_primary ? (
                          <Badge tone="ok">PRIMARY</Badge>
                        ) : idx.is_unique ? (
                          <Badge tone="info">UNIQUE</Badge>
                        ) : (
                          <Badge tone="neutral">INDEX</Badge>
                        )}
                      </td>
                      <td data-label="Definisi SQL">
                        <code>{idx.definition}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableWrap>
          )}
        </>
      )}
    </QueryState>
  )
}
