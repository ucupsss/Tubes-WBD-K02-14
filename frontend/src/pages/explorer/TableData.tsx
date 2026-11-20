import { Link, useParams, useSearchParams } from 'react-router-dom'
import { useTableData } from '../../api/hooks'
import { Badge, PageHeader, QueryState } from '../../components/ui'

/**
 * Halaman Penampil Data Tabel Berpaginasi (/db/:dbID/tables/:schema/:table/data) — Paket B4.
 * Tabel lebar memakai kontainer scroll horizontal (.data-grid-wrap) supaya tetap nyaman
 * dilihat di layar 360px (mobile) maupun 1280px (desktop).
 */
export function TableDataPage() {
  const { dbID, schema = '', table = '' } = useParams<{ dbID: string; schema: string; table: string }>()
  const [searchParams, setSearchParams] = useSearchParams()
  const id = Number(dbID)

  const page = Math.max(1, Number(searchParams.get('page') || '1') || 1)
  const pageSize = Math.min(200, Math.max(1, Number(searchParams.get('page_size') || '50') || 50))
  const query = useTableData(id, schema, table, page, pageSize)

  const goToPage = (nextPage: number, nextSize = pageSize) => {
    setSearchParams({ page: String(nextPage), page_size: String(nextSize) })
  }

  return (
    <QueryState query={query}>
      {({ database, data_page }) => {
        const structUrl = `/db/${id}/tables/${encodeURIComponent(data_page.schema)}/${encodeURIComponent(data_page.table)}`
        return (
          <>
            <div className="breadcrumb small muted">
              <Link to="/">Dashboard</Link> <span>/</span> <Link to={`/db/${id}`}>{database.name}</Link> <span>/</span>{' '}
              <Link to={structUrl}>
                {data_page.schema}.{data_page.table}
              </Link>{' '}
              <span>/</span> <strong>Data</strong>
            </div>

            <PageHeader
              title={`${data_page.schema}.${data_page.table} · Data Baris`}
              actions={
                <div className="form-actions">
                  <Link className="btn btn-ghost" to={structUrl}>
                    Lihat Struktur
                  </Link>
                  <Link className="btn btn-ghost" to={`/db/${id}`}>
                    Katalog Skema
                  </Link>
                </div>
              }
            />

            <div className="pagination-bar card">
              <div>
                Total baris: <strong>{data_page.total_rows.toLocaleString('id-ID')}</strong> · Halaman{' '}
                <strong>{data_page.page}</strong> dari <strong>{Math.max(1, data_page.total_pages)}</strong>
                {data_page.pk_columns.length > 0 && (
                  <span className="muted small"> · Urut PK: ({data_page.pk_columns.join(', ')})</span>
                )}
              </div>
              <div className="form-actions">
                <label className="small muted">
                  Baris/hal:{' '}
                  <select
                    aria-label="Baris per halaman"
                    value={pageSize}
                    onChange={e => goToPage(1, Number(e.target.value))}
                    style={{ width: 'auto', minHeight: 36, padding: '4px 8px' }}
                  >
                    {[25, 50, 100, 200].map(sz => (
                      <option key={sz} value={sz}>
                        {sz}
                      </option>
                    ))}
                  </select>
                </label>
                <button
                  type="button"
                  className="btn btn-ghost"
                  disabled={!data_page.has_prev}
                  onClick={() => goToPage(data_page.prev_page)}
                >
                  ← Sebelumnya
                </button>
                <button
                  type="button"
                  className="btn btn-ghost"
                  disabled={!data_page.has_next}
                  onClick={() => goToPage(data_page.next_page)}
                >
                  Berikutnya →
                </button>
              </div>
            </div>

            {data_page.rows.length === 0 ? (
              <div className="card empty">
                <p>Tabel ini belum memiliki baris data pada halaman ini.</p>
              </div>
            ) : (
              <div className="table-wrap data-grid-wrap">
                <table className="data-grid">
                  <thead>
                    <tr>
                      {data_page.columns.map(col => (
                        <th key={col}>
                          {col}{' '}
                          {data_page.pk_columns.includes(col) && (
                            <Badge tone="ok">PK</Badge>
                          )}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {data_page.rows.map((row, rIdx) => (
                      <tr key={rIdx}>
                        {row.map((val, cIdx) => (
                          <td key={cIdx}>
                            {val === 'NULL' ? <span className="muted">NULL</span> : <code>{val}</code>}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )
      }}
    </QueryState>
  )
}
