import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { createMigration, keys, updateMigration, useMigration, validateMigration } from '../../api/hooks'
import type { MigrationInput, MigrationItem } from '../../api/types'
import { Alert, Field, PageHeader, QueryState, useFlash } from '../../components/ui'
import { useSubmit } from '../../lib/form'
import { Breadcrumb, useCurrentDatabase } from './MigrationList'

const EMPTY: MigrationInput = {
  title: '',
  up_sql: '',
  down_sql: '',
  irreversible: false,
  irreversible_reason: '',
  exec_mode: 'normal',
  batch_table: '',
  batch_column: 'id',
  batch_size: 10000,
}

function inputFrom(m: MigrationItem): MigrationInput {
  const r = m.revision
  return {
    title: m.title,
    up_sql: r.up_sql,
    down_sql: r.down_sql ?? '',
    irreversible: r.irreversible,
    irreversible_reason: r.irreversible_reason ?? '',
    exec_mode: r.exec_mode,
    batch_table: r.batch_table ?? '',
    batch_column: r.batch_column ?? 'id',
    batch_size: r.batch_size ?? 10000,
  }
}

/** /db/:dbID/migrations/new */
export function MigrationNewPage() {
  const dbId = Number(useParams().dbID)
  return <MigrationFormPage dbId={dbId} migration={null} />
}

/** /db/:dbID/migrations/:id/edit */
export function MigrationEditPage() {
  const params = useParams()
  const dbId = Number(params.dbID)
  const id = Number(params.id)
  const query = useMigration(dbId, id)
  return <QueryState query={query}>{m => <MigrationFormPage key={m.revision.id} dbId={dbId} migration={m} />}</QueryState>
}

function MigrationFormPage({ dbId, migration }: { dbId: number; migration: MigrationItem | null }) {
  const db = useCurrentDatabase(dbId)
  const navigate = useNavigate()
  const flash = useFlash()
  const qc = useQueryClient()
  const [input, setInput] = useState<MigrationInput>(migration ? inputFrom(migration) : EMPTY)
  const [checked, setChecked] = useState(false)
  const save = useSubmit()
  const check = useSubmit()
  const fields = { ...check.fields, ...save.fields }
  const hasErrors = save.error !== '' || check.error !== '' || Object.keys(fields).length > 0
  const set = <K extends keyof MigrationInput>(k: K, v: MigrationInput[K]) => {
    setChecked(false)
    if (hasErrors) {
      // Pesan lama bisa menyesatkan setelah isinya diubah; validasi ulang terjadi saat simpan/cek.
      save.reset()
      check.reset()
    }
    setInput(prev => ({ ...prev, [k]: v }))
  }
  const frozen = migration?.revision.frozen_at != null
  const batch = input.exec_mode === 'batch'

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    check.reset()
    const out: { saved?: MigrationItem } = {}
    const ok = await save.run(async () => {
      out.saved = migration ? await updateMigration(dbId, migration.id, input) : await createMigration(dbId, input)
    })
    if (ok && out.saved) {
      const m = out.saved
      await qc.invalidateQueries({ queryKey: keys.migrations(dbId) })
      flash.success(
        migration && m.revision.revision_no !== migration.revision.revision_no
          ? `Revisi ${m.revision.revision_no} dibuat. Revisi lama tetap tersimpan di riwayat.`
          : 'Migration disimpan sebagai draft',
      )
      navigate(`/db/${dbId}/migrations/${m.id}`)
    }
  }

  const onValidate = async () => {
    if (!migration) {
      return
    }
    save.reset()
    setChecked(await check.run(() => validateMigration(dbId, migration.id, input)))
  }

  const back = migration ? `/db/${dbId}/migrations/${migration.id}` : `/db/${dbId}/migrations`

  return (
    <>
      <Breadcrumb dbId={dbId} dbName={db?.name}>
        <Link to={`/db/${dbId}/migrations`}>Migration</Link> <span>/</span>{' '}
        <strong>{migration ? `Ubah #${migration.id}` : 'Baru'}</strong>
      </Breadcrumb>
      <PageHeader title={migration ? `Ubah: ${migration.title}` : 'Migration baru'} />

      {db && db.level !== 'edit' && <Alert>Anda tidak punya akses edit ke database ini.</Alert>}
      {frozen && (
        <Alert kind="info">
          Revisi {migration?.revision.revision_no} sudah pernah diajukan sehingga tidak dapat diubah. Menyimpan form ini
          membuat <strong>revisi baru</strong> yang perlu diajukan dan di-approve lagi.
        </Alert>
      )}

      <form className="card form-grid" onSubmit={e => void onSubmit(e)} noValidate>
        <Field label="Judul" error={fields.title}>
          <input value={input.title} onChange={e => set('title', e.target.value)} maxLength={200} required />
        </Field>

        <fieldset className="fieldset">
          <legend className="field-label">Mode eksekusi</legend>
          <div className="form-actions">
            <label className="check">
              <input type="radio" name="exec_mode" checked={!batch} onChange={() => set('exec_mode', 'normal')} />
              <span>Normal (satu transaction)</span>
            </label>
            <label className="check">
              <input type="radio" name="exec_mode" checked={batch} onChange={() => set('exec_mode', 'batch')} />
              <span>Batch (per rentang, dengan checkpoint)</span>
            </label>
          </div>
          {fields.exec_mode && <span className="field-msg">{fields.exec_mode}</span>}
        </fieldset>

        {batch && (
          <div className="form-row">
            <Field label="Tabel" error={fields.batch_table} hint="Tabel yang dibagi per rentang">
              <input value={input.batch_table} onChange={e => set('batch_table', e.target.value)} placeholder="big_events" />
            </Field>
            <Field label="Kolom kunci" error={fields.batch_column} hint="Kolom bilangan bulat terurut">
              <input value={input.batch_column} onChange={e => set('batch_column', e.target.value)} placeholder="id" />
            </Field>
            <Field label="Ukuran batch" error={fields.batch_size} hint="Jumlah nilai kunci per batch">
              <input
                type="number"
                min={1}
                value={Number.isFinite(input.batch_size) ? input.batch_size : ''}
                onChange={e => set('batch_size', e.target.valueAsNumber)}
              />
            </Field>
          </div>
        )}

        <Field
          label="SQL up"
          error={fields.up_sql}
          hint={batch ? 'Wajib memuat :from dan :to, mis. WHERE id > :from AND id <= :to' : 'Dijalankan saat apply/forward'}
        >
          <textarea
            className="sql-editor"
            rows={10}
            spellCheck={false}
            value={input.up_sql}
            onChange={e => set('up_sql', e.target.value)}
            placeholder={batch ? 'UPDATE big_events SET payload = payload + 1 WHERE id > :from AND id <= :to' : 'ALTER TABLE …'}
          />
        </Field>

        <label className="check">
          <input type="checkbox" checked={input.irreversible} onChange={e => set('irreversible', e.target.checked)} />
          <span>Tidak dapat di-rollback (irreversible)</span>
        </label>

        {input.irreversible ? (
          <Field label="Alasan irreversible" error={fields.irreversible_reason} hint="Ditampilkan ke leader saat review">
            <textarea rows={3} value={input.irreversible_reason} onChange={e => set('irreversible_reason', e.target.value)} />
          </Field>
        ) : (
          <Field
            label="SQL down"
            error={fields.down_sql}
            hint={batch ? 'Revert per batch; wajib memuat :from dan :to' : 'Dijalankan saat rollback'}
          >
            <textarea
              className="sql-editor"
              rows={8}
              spellCheck={false}
              value={input.down_sql}
              onChange={e => set('down_sql', e.target.value)}
            />
          </Field>
        )}

        {save.error && <Alert>{save.error}</Alert>}
        {check.error && <Alert>{check.error}</Alert>}
        {checked && <Alert kind="success">Sintaks dan aturan SQL valid.</Alert>}

        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={save.pending}>
            {save.pending ? 'Menyimpan…' : frozen ? 'Simpan sebagai revisi baru' : 'Simpan draft'}
          </button>
          {migration && (
            <button type="button" className="btn btn-ghost" onClick={() => void onValidate()} disabled={check.pending}>
              {check.pending ? 'Memeriksa…' : 'Cek validasi'}
            </button>
          )}
          <Link className="btn btn-ghost" to={back}>
            Batal
          </Link>
        </div>
      </form>
    </>
  )
}
