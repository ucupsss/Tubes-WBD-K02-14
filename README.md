# Tubes WBD

Tubes WBD adalah platform manajemen database berbasis web untuk mengelola beberapa database PostgreSQL secara aman: login dan access control, menjelajahi schema dan data, lalu menyusun dan menjalankan migration di dalam transaction.

Backend ditulis dengan Go, dan halaman dirender di server memakai `html/template`. Pada milestone ini, database target yang didukung hanya PostgreSQL.

Alur minimum yang didukung:

```
Login → Access Control → Pilih Database → Lihat Schema/Data
      → Buat Migration → Jalankan → Commit/Rollback
      → Riwayat Migration diperbarui
```

## Menjalankan

Butuh Go 1.24+, Node.js 20+ (untuk frontend), Docker Desktop, dan `make` (Git Bash). Pastikan port di bawah tidak sedang dipakai Postgres lokal atau container lain.

```bash
cp .env.example .env
# isi APP_SECRET_KEY (openssl rand -base64 32) dan BOOTSTRAP_ADMIN_PASSWORD
make up
make web-install  # sekali saja: dependensi frontend
make web-build    # membangun SPA ke web/dist (di-embed ke binary Go)
make run          # API di http://localhost:8080
make worker       # proses terpisah yang mengambil job migration
make seed         # user, group, koneksi, dan grant untuk demo
make k6           # stress test API baca; butuh k6 dan server yang sudah jalan
```

Saat mengembangkan frontend, jalankan `make run` di satu terminal dan `make web-dev` di terminal lain, lalu buka `http://localhost:5173` (Vite meneruskan `/api` ke server Go, jadi tidak perlu CORS).

`make run` menjalankan migrasi platform secara otomatis. Jika ada file migrasi baru yang nomornya lebih kecil dari yang sudah diterapkan, jalankan `make db-reset`.

| Perintah                | Fungsi                                                                    |
|-------------------------|---------------------------------------------------------------------------|
| `make up` / `make down` | Menyalakan / mematikan Postgres                                           |
| `make db-reset`         | Menghapus volume lalu menyalakan ulang dari kosong                        |
| `make worker`       | Worker antrian migration, proses terpisah dari API                        |
| `make k6`           | Stress test k6 pada API baca. Butuh [k6](https://grafana.com/docs/k6/) di PATH |
| `make web-check`        | Frontend: type-check, unit test, dan build                                |
| `make test`             | Unit test                                                                 |
| `make test-integration` | Unit test + integration test (butuh `make up` dan `TEST_DATABASE_URL`)    |
| `make check`            | `lint` + integration test. Wajib lolos sebelum membuka PR                 |

Port lokal: aplikasi `8080`, platform-db `5432`, target-db-1 `5433`, target-db-2 `5434`, test-db `5440`.

## Akun demo

Saat `make run` pertama kali dan tabel user masih kosong, admin dibuat dari `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD`.

`make seed` menambahkan:

| User  | Password      | Group              |
|-------|---------------|--------------------|
| alice | `password123` | dev                |
| bob   | `password123` | dev dan analyst    |
| carol | `password123` | analyst            |

Koneksi `target-db-1` (port 5433, database `target1`) dan `target-db-2` (port 5434, database `target2`) memakai user/password `target`. Grant: `dev` punya akses edit ke db1; `analyst` punya akses view ke db1 dan db2. Bob ada di kedua group, sehingga akses efektif Bob ke db1 adalah edit.

Password koneksi target di seed hanya untuk demo lokal.

## Keputusan desain

| #   | Keputusan |
|-----|-----------|
| D1  | Akses efektif adalah level **tertinggi** dari semua group user pada database tersebut (`edit` > `view` > tidak ada). |
| D2  | Platform Admin **tidak** otomatis punya akses ke database target. Admin hanya mengelola user, group, koneksi, dan grant; untuk membuka database, admin harus menjadi anggota group yang punya grant. |
| D3  | Session: idle timeout 30 menit, absolute expiration 8 jam, rotasi tiap 15 menit, grace period 30 detik. Semuanya bisa diubah lewat env. Waktu aktivitas terakhir (`last_seen_at`) diperbarui paling sering sekali per menit, jadi idle timeout berpresisi ±1 menit. |
| D4  | Session identifier adalah 32 byte acak (`crypto/rand`). Database hanya menyimpan hash SHA-256-nya. |
| D5  | Token lama yang dikirim selama grace period tetap dilayani, dan tidak dirotasi lagi. Setelah grace period habis, token lama ditolak. |
| D6  | Kenaikan privilege (dijadikan admin, masuk group, atau grant naik) menaikkan `privilege_version`, sehingga session dirotasi pada request berikutnya. |
| D7  | Menonaktifkan user, mencabut status admin, atau reset password oleh admin mencabut semua session user tersebut. Keluar dari group atau kehilangan grant langsung berlaku karena hak akses dibaca ulang dari database di setiap request. |
| D8  | User tidak pernah dihapus permanen. Penonaktifan mengisi `is_active = false`. Admin tidak bisa mencabut status admin atau menonaktifkan akunnya sendiri. |
| D9  | Koneksi yang sudah punya riwayat migration tidak bisa dihapus (`ON DELETE RESTRICT`). |
| D10 | Nomor versi diberikan saat eksekusi berhasil: `max(version) + 1` per database. Draft dan migration gagal belum punya versi. Setiap percobaan dicatat di `migration_runs`. |
| D11 | Migration `success` tidak bisa diubah, dihapus, atau dijalankan ulang. Draft bisa diubah, dihapus, dan dijalankan. Migration `failed` bisa diubah dan dijalankan ulang, tetapi tidak bisa dihapus. |
| D12 | Eksekusi untuk satu database diserialisasi dengan `SELECT ... FOR UPDATE` pada baris `db_connections`. |
| D13 | SQL yang berisi `BEGIN` / `COMMIT` / `ROLLBACK` / `SAVEPOINT`, `CREATE INDEX CONCURRENTLY`, `VACUUM`, atau `CREATE DATABASE` ditolak. Kata yang sama di dalam string atau komentar tetap diperbolehkan. |
| D14 | Database explorer hanya membaca: memakai transaction `READ ONLY`, `statement_timeout`, identifier yang di-quote, dan data berpaginasi. |
| D15 | Request tanpa login diarahkan ke `/login`. User yang sudah login tetapi tidak berhak mendapat 403, termasuk untuk ID database yang tidak ada (supaya keberadaan database tidak bocor). Target database yang tidak bisa dihubungi menghasilkan 502 dengan pesan tanpa password/DSN. |
| D16 | Username 3–32 karakter (huruf, angka, `.`, `_`, `-`). Password minimal 8 karakter dan maksimal 72 byte (batas bcrypt; byte sesudahnya ditolak, bukan dipotong diam-diam). |
| D17 | Satu user boleh punya beberapa session sekaligus (mis. dua perangkat). Login ulang di browser yang sama mencabut session lama di browser itu. |
| D18 | Menghapus group ikut menghapus keanggotaan dan grant group tersebut (`ON DELETE CASCADE`). |
| D19 | Explorer menampilkan semua schema kecuali `pg_catalog`, `information_schema`, dan `_dbmp` (tabel internal platform). Data ditampilkan 50 baris per halaman (maksimal 200) dan diurutkan berdasarkan primary key bila ada. |
| D20 | Analisis EXPLAIN hanya untuk migration `draft` atau `failed` dan butuh akses edit, karena `EXPLAIN ANALYZE` benar-benar mengeksekusi statement. Detailnya ada di bagian [Analisis EXPLAIN](#analisis-explain). |
| D21 | Frontend adalah Single-Page Application: React 18 + Vite + TypeScript + React Router + TanStack Query, folder `frontend/`. Hasil build ada di `web/dist` dan disajikan server Go (`internal/spa`). Path yang bukan route server dan bukan file dijawab `index.html` supaya router SPA menanganinya; file ber-ekstensi yang tidak ada tetap 404 (HTML tidak menyamar sebagai JS/CSS). |
| D22 | API berformat JSON di bawah `/api`, memakai cookie session yang sama. Mutasi wajib membawa header `X-CSRF-Token`: token session dari `GET /api/me`, atau token pre-login dari `GET /api/csrf` (double-submit cookie). Error berbentuk `{"error":{"code","message","details"}}`; request tanpa login ke `/api` dijawab 401 JSON, bukan redirect. |
| D27 | Setiap perubahan database (apply, forward, rollback) butuh approval dari leader **setiap** group yang punya grant view atau edit ke database tersebut. Satu baris approval per (execution, ronde, group). Satu **reject** menolak seluruh execution. |
| D28 | Leader group (`groups.leader_id`) harus anggota aktif group. Leader tidak bisa dikeluarkan dari group atau dinonaktifkan sebelum diganti. Leader boleh meng-approve perubahan yang ia ajukan sendiri, dan satu keputusan mewakili semua group yang ia pimpin. Group tanpa leader **tidak menolak** pengajuan: execution tetap menunggu sampai leader ditetapkan. Platform Admin tidak otomatis boleh approve (konsisten dengan D2). |
| D33 | Filter **Hide rollback** di riwayat eksekusi menyembunyikan pasangan `(execution yang dibatalkan, rollback sukses yang membatalkannya)` berdasarkan **Execution ID** (`rolls_back_execution_id`), bukan nama migration. Eksekusi gagal dan rollback gagal selalu tetap tampil. Filter hanya mengubah tampilan dan tidak menghapus data. |
| D36 | **Resume dan revert batch**: resume melanjutkan Execution ID yang sama dari checkpoint terakhir di `_dbmp.batch_checkpoints` tanpa mengulang batch yang sudah committed dan memakai approval yang sudah ada. Rollback pada eksekusi batch yang sudah committed (atau `partial`) menjalankan SQL down per batch yang sudah committed dalam **urutan terbalik** (`batch_no` terbesar ke terkecil). Bila revert gagal di tengah, status menjadi `partially_reverted` dan dapat di-resume dengan aturan yang sama. |
| D37 | **Bukti eksekusi** disimpan di database target dalam transaction yang sama dengan perubahan: `_dbmp.executions` (mode biasa) dan `_dbmp.batch_checkpoints` (mode batch). Tabel `execution_batches` di database platform merupakan cermin untuk tampilan UI. |
| D39 | **Mode batch**: migration mendefinisikan `batch_table`, `batch_column` (wajib bertipe integer di katalog target), dan `batch_size`. SQL up dan down wajib memuat placeholder `:from` (batas bawah eksklusif) dan `:to` (batas atas inklusif). Platform mencari batas atas tiap batch lewat query berbatas `LIMIT`, lalu mengganti `:from` dan `:to` dengan angka literal `int64` tanpa memuat seluruh dataset ke memori. |
| D40 | Approval dicek ulang dari grant dan leader **saat ini** sebelum job berjalan. Bila ada group baru yang punya akses atau leader berganti, approval dicabut dan execution kembali ke `pending_review` pada ronde berikutnya (bukan `failed`). Approval tidak perlu diulang untuk retry otomatis dan resume dari checkpoint. |
| D41 | Audit log berupa event log terstruktur `audit.*` ke stdout, tidak disimpan di database platform. Riwayat migration di database tidak terpengaruh filter tampilan apa pun. |

### Kode status di halaman error

Halaman error menampilkan kode status, namanya, dan penjelasan singkat.

| Kode | Nama | Kapan muncul |
|------|------|--------------|
| — (redirect ke `/login`) | Belum login | Membuka halaman terproteksi tanpa session yang berlaku (belum login, session idle/absolute habis, atau sudah dicabut). |
| 400 | Permintaan Tidak Valid | Format permintaan salah. Kesalahan isian form ditampilkan langsung di form (422). |
| 401 | — | Login gagal; form login ditampilkan lagi dengan pesan generik. |
| 403 | Akses Ditolak | Sudah login tetapi tidak berhak (bukan admin, akses database kurang), atau token CSRF tidak valid. |
| 404 | Tidak Ditemukan | Halaman, table, atau migration tidak ada (termasuk alamat URL yang salah ketik). |
| 409 | Konflik | Aksi bertentangan dengan kondisi data, mis. nama sudah dipakai, migration `success` diubah, atau koneksi yang punya riwayat migration dihapus. |
| 502 | Database Tidak Dapat Dihubungi | Database target mati atau menolak koneksi. |
| 500 | Kesalahan Server | Kesalahan tak terduga; detailnya hanya dicatat di log server. |

### Parameter session

| Env                         | Default | Arti |
|-----------------------------|---------|------|
| `SESSION_IDLE_TIMEOUT`      | `30m`   | Session berakhir bila tidak ada request selama waktu ini. |
| `SESSION_ABSOLUTE_TIMEOUT`  | `8h`    | Umur maksimum session sejak login. Tidak diperpanjang oleh aktivitas maupun rotasi. |
| `SESSION_ROTATION_INTERVAL` | `15m`   | Session identifier diganti secara berkala (selain saat login dan saat privilege naik). |
| `SESSION_GRACE_PERIOD`      | `30s`   | Identifier lama masih diterima sebentar setelah rotasi, untuk request yang berjalan bersamaan. |

Cookie session: `dbmp_session` (`HttpOnly`, `SameSite=Lax`, `Secure` bila `APP_ENV=production`, tanpa `Expires`). Nama cookie tetap; nilainya diganti setiap rotasi. Logout (`POST /logout`) menonaktifkan session di backend, bukan hanya menghapus cookie.

Password di-hash dengan bcrypt (cost 12). Login yang gagal selalu memberi pesan yang sama. Password, session identifier, dan token CSRF tidak pernah muncul di URL, response, maupun log; logger hanya mencatat method, path, status, dan durasi.

### CSRF

Semua `POST/PUT/PATCH/DELETE` wajib membawa token, lewat hidden field `_csrf` atau header `X-CSRF-Token`. Token yang kosong atau salah menghasilkan 403.

- Setelah login, token berasal dari session (`sessions.csrf_token`) dan dibandingkan secara constant-time.
- Form login (belum ada session) memakai cookie double-submit `dbmp_csrf_pre`, ditambah pengecekan bahwa header `Origin`/`Referer` berasal dari host yang sama.

### Matriks hak akses

Aturan aksi hanya didefinisikan di satu tempat: `internal/authz/policy.go`. Aksi yang tidak terdaftar selalu ditolak. Route `/db/...` mengikuti grant group.

| Route                                                   | Akses minimum    |
|---------------------------------------------------------|------------------|
| `GET/POST /login`                                       | publik           |
| `POST /logout`, `GET /` (dashboard)                     | login            |
| `/admin/users`, `/admin/groups`                         | platform admin   |
| `/admin/databases`, `/admin/groups/{id}/access`         | platform admin   |
| `GET /db/{dbID}/...` (schema dan data)                  | view             |
| `GET /db/{dbID}/migrations`, `GET .../{id}`             | view             |
| buat, ubah, jalankan, hapus draft migration             | edit             |
| analisis EXPLAIN (`POST .../migrations/{id}/explain`)   | edit             |

Menyembunyikan tombol di antarmuka bukan otorisasi; setiap aksi dicek ulang di backend.

## Frontend (SPA) dan API

Seluruh halaman login, administrasi (user, group, leader, akses database, koneksi database), inbox approval, dashboard, **database explorer** (katalog skema, struktur tabel, data baris berpaginasi, analisis EXPLAIN), serta **eksekusi & riwayat migration** (monitor job asinkron, filter Hide rollback, detail execution, attempt, dan checkpoint batch) disajikan sebagai SPA.

- **Responsif**: navigasi menjadi menu hamburger di layar sempit, tabel menjadi kartu bertumpuk di mobile, tabel data lebar (`.data-grid-wrap`) scroll horizontal di dalam komponennya (bukan di halaman), dan semua kontrol berukuran minimal 44 px. Diuji pada lebar 360 px dan 1280 px.
- **Satu pintu ke backend**: semua request lewat `apiFetch` (`frontend/src/api/client.ts`) yang memasang token CSRF, menyeragamkan error, dan mengosongkan sesi saat server menjawab 401.
- **Format error API**:

| HTTP | `code`                         | Arti |
|------|--------------------------------|------|
| 400  | `invalid`                      | Input tidak valid; `details.fields` berisi pesan per field |
| 401  | `unauthenticated`, `invalid_credentials` | Belum login atau session habis; kredensial salah (pesan sengaja generik) |
| 403  | `forbidden`                    | Tidak berhak, termasuk token CSRF salah dan ID database yang tidak ada (D15) |
| 404  | `not_found`                    | Data atau path tidak ada |
| 409  | `conflict`                     | Bentrok aturan; `details.reason` berisi kode mesin (mis. `is_leader`, `leader_not_member`, `not_analyzable`) |
| 502  | `target_unavailable`           | Database target tidak dapat dihubungi |
| 500  | `internal`                     | Kesalahan server; detail hanya ada di log, tidak pernah di respons |

- **Endpoint** (semua JSON): `GET /api/csrf`, `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/me`; `GET|POST /api/admin/users`, `GET|PUT /api/admin/users/{id}`, `POST /api/admin/users/{id}/password`; `GET|POST /api/admin/groups`, `GET|PUT|DELETE /api/admin/groups/{id}`, `POST /api/admin/groups/{id}/members`, `DELETE /api/admin/groups/{id}/members/{userID}`, `PUT /api/admin/groups/{id}/leader`, `GET|PUT /api/admin/groups/{id}/access`; `GET|POST /api/admin/databases`, `GET|PUT|DELETE /api/admin/databases/{id}`, `POST /api/admin/databases/test`, `POST /api/admin/databases/{id}/test`; `GET /api/approvals`, `POST /api/executions/{id}/approve|reject`; `GET /api/databases`, `GET /api/databases/{dbID}/schema`, `GET /api/databases/{dbID}/tables/{schema}/{table}`, `GET /api/databases/{dbID}/tables/{schema}/{table}/data?page=&page_size=`; `POST /api/databases/{dbID}/migrations/{id}/explain`.

## Approval dan leader

Leader ditetapkan Platform Admin di halaman detail group. Saat sebuah execution diajukan, sistem membuat satu baris approval untuk tiap group yang punya akses ke database itu. Leader melihat antreannya di menu **Approval** (`GET /api/approvals`) dan memutuskan lewat `POST /api/executions/{id}/approve|reject`. Execution baru bisa lanjut ke antrean setelah semua group menyetujui; pengguna yang mengajukan lalu menekan tombol Jalankan (tidak otomatis).

Paket `internal/approval` tidak mengubah status execution sendiri: ia memanggil interface `Executions` yang diimplementasikan modul execution, dan menyediakan `OpenRound`, `Verify`, dan `Revoke` untuk worker (revalidasi, D40).

## Log terstruktur

Seluruh log aplikasi berupa satu baris JSON ke stdout (`internal/logx`). Pesan menjadi field `event`; `correlation_id` dibaca dari header `X-Correlation-Id` (bila valid) atau dibuat server, dikirim balik di header respons, dan ikut di semua log request; `actor` terisi setelah session dikenali. Nilai sensitif (password, token, session, cookie, CSRF, DSN) diganti `[REDACTED]`, dan nilai string dipotong 256 karakter. Contoh keluaran nyata:

```json
{"time":"2026-11-17T18:56:17.5052763+07:00","level":"INFO","event":"auth.login","user_id":1,"status":"ok","correlation_id":"demo-corr-1"}
{"time":"2026-11-17T18:56:17.5968612+07:00","level":"INFO","event":"http.request","method":"GET","path":"/api/me","status":200,"duration_ms":4,"correlation_id":"f254b3df5b4051ea16b24520ecbec15b","actor":"admin"}
```

Helper `logx.SQL` mencatat satu eksekusi SQL (query, parameter yang disamarkan, durasi, jumlah row, SQLSTATE), dan `logx.Audit` mencatat event `audit.*`. Atribut `trace_id` dan `span_id` terisi otomatis begitu paket tracing mendaftarkan pembacanya lewat `logx.SetTraceExtractor`.

## Distributed Tracing (OpenTelemetry) dan Korelasi Log

Paket `internal/telemetry` menginisialisasi OpenTelemetry SDK (exporter stdout/console, propagator W3C `TraceContext`), HTTP middleware, fungsi propagasi queue (`telemetry.Inject` dan `telemetry.Extract`), serta `telemetry.PgxTracer()` (`pgx.QueryTracer` & `pgx.BatchTracer`) yang dipasang di seluruh pool database platform maupun target.

Alur span dan korelasi log:
1. Request masuk melewati `CorrelationMiddleware` lalu `telemetry.HTTPMiddleware` yang membuka root span HTTP (`POST /api/executions/{id}/enqueue`).
2. Saat job dimasukkan ke antrean, `telemetry.Inject(ctx)` mengekstrak `traceparent` dan `correlation_id` ke `migration_jobs.trace_context`.
3. Worker memulihkan trace context menggunakan `telemetry.Extract(ctx, carrier)` sehingga span `job.process`, `lock.acquire`, `revalidate`, `target.batch`, dan `db.query` memiliki **`trace_id` yang sama** dengan request HTTP pembukanya.
4. `PgxTracer()` otomatis membuat span `db.query` dan memanggil `logx.SQL`, sehingga setiap baris log SQL mencantumkan `trace_id`, `span_id`, `correlation_id`, `execution_id`, `batch_no`, durasi, jumlah row, serta `sqlstate` bila terjadi error.

Contoh keluaran nyata log JSON dan span OpenTelemetry yang berkorelasi:

```json
{"time":"2026-11-19T23:58:10.412Z","level":"INFO","event":"sql.exec","query":"UPDATE big_events SET v = v + 1 WHERE id > 100 AND id <= 200","duration_ms":14,"params":["100","200"],"rows":100,"status":"ok","migration_id":11,"execution_id":22,"job_id":33,"attempt":1,"batch_no":2,"correlation_id":"corr-abc-123","actor":"daniel","database":"7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
{"time":"2026-11-19T23:58:10.415Z","level":"INFO","event":"batch.committed","execution_id":22,"direction":"apply","batch_no":2,"from":100,"to":200,"rows":100,"status":"ok","correlation_id":"corr-abc-123","actor":"daniel","database":"7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"a2fb4a1d1a96d312"}
```

Contoh potongan span OpenTelemetry yang berkorelasi dengan log di atas (`TraceID: 4bf92f3577b34da6a3ce929d0e0e4736`):

```json
{
  "Name": "db.query",
  "SpanContext": {
    "TraceID": "4bf92f3577b34da6a3ce929d0e0e4736",
    "SpanID": "00f067aa0ba902b7"
  },
  "Parent": {
    "TraceID": "4bf92f3577b34da6a3ce929d0e0e4736",
    "SpanID": "a2fb4a1d1a96d312"
  },
  "SpanKind": 3,
  "Attributes": [
    {"Key": "db.system", "Value": {"Type": "STRING", "Value": "postgresql"}},
    {"Key": "db.statement", "Value": {"Type": "STRING", "Value": "UPDATE big_events SET v = v + 1 WHERE id > 100 AND id <= 200"}},
    {"Key": "db.target", "Value": {"Type": "STRING", "Value": "7"}},
    {"Key": "execution_id", "Value": {"Type": "INT64", "Value": 22}},
    {"Key": "batch_no", "Value": {"Type": "INT64", "Value": 2}},
    {"Key": "db.duration_ms", "Value": {"Type": "INT64", "Value": 14}},
    {"Key": "db.rows_affected", "Value": {"Type": "INT64", "Value": 100}}
  ]
}
```

## Mode Eksekusi per Batch (`internal/batch`)

Untuk perubahan data berukuran besar, migration dapat dijalankan dalam mode `batch` (`exec_mode = 'batch'`) agar tidak menahan lock tabel dalam satu transaksi raksasa:

- **Konvensi placeholder `:from` dan `:to` (D39)**:
  - Migration mendefinisikan `batch_table` (mis. `big_events`), `batch_column` (kolom integer unik dan tidak berubah selama migrasi, mis. `id`), dan `batch_size` (> 0).
  - `up_sql` dan `down_sql` wajib memuat placeholder `:from` (batas bawah **eksklusif**, `> :from`) dan `:to` (batas atas **inklusif**, `<= :to`), misalnya:
    ```sql
    -- SQL Up:
    UPDATE big_events SET v = v + 1 WHERE id > :from AND id <= :to;
    -- SQL Down:
    UPDATE big_events SET v = v - 1 WHERE id > :from AND id <= :to;
    ```
  - Sebelum mulai, `batch.Runner` memverifikasi keberadaan tabel dan memastikan `batch_column` bertipe integer (`smallint`, `integer`, `bigint`) di `information_schema.columns` target, lalu men-sanitize nama tabel/kolom dengan `pgx.Identifier`.
  - Di setiap iterasi, platform hanya mengambil batas atas batch berikutnya tanpa memuat seluruh dataset ke memori:
    ```sql
    SELECT max("id") FROM (SELECT "id" FROM "public"."big_events" WHERE "id" > $1 ORDER BY "id" ASC LIMIT $2) AS _dbmp_sub
    ```
  - Placeholder `:from` dan `:to` (yang berada di luar string literal, komentar, dollar-quote, dan type cast `::`) diganti dengan angka literal `int64`.
- **Transaction & Checkpoint per Batch (D36, D37)**:
  - Setiap batch dijalankan dalam satu transaction tersendiri di database target dengan `statement_timeout` (default 30s) dan `lock_timeout` (default 5s).
  - Di dalam transaction yang sama dengan eksekusi SQL batch, runner menyisipkan bukti checkpoint ke `_dbmp.batch_checkpoints` (`PRIMARY KEY (execution_id, direction, batch_no)`), lalu melakukan `COMMIT`. Setelah commit di target berhasil, progress dicerminkan ke tabel `execution_batches` di database platform.
- **Kegagalan Sebagian (`partial`), Resume, dan Revert Terbalik**:
  - Bila batch ke-$k$ gagal, hanya transaction batch ke-$k$ yang di-rollback; batch $1 \dots (k-1)$ tetap committed dan status execution menjadi `partial`.
  - **Resume** (`POST /api/executions/{id}/resume`) membaca checkpoint terakhir dari `_dbmp.batch_checkpoints` dan melanjutkan dari `to_value` terakhir tanpa mengulang batch yang sudah committed.
  - **Revert / Rollback Batch** membaca seluruh checkpoint `direction='apply'` yang sudah committed lalu menjalankan `down_sql` per batch dalam **urutan terbalik** (`batch_no` terbesar ke terkecil), sambil mencatat checkpoint `direction='revert'` di setiap transaction batch. Bila gagal di tengah proses revert, status menjadi `partially_reverted` dan dapat di-resume hingga tuntas.

## Migration

Satu migration punya revisi SQL. Mengajukan revisi membuat execution `pending_review` dan membuka approval. API tidak menjalankan SQL di database target.

Setelah execution berstatus `approved`, `POST /api/executions/{id}/enqueue` menulis satu baris `migration_jobs` dan menjawab **202** dengan `job_id`. Publish kedua untuk execution yang sama tidak membuat job baru. `POST …/resume` melanjutkan execution `partial` atau `partially_reverted` tanpa approval baru. `POST …/cancel` hanya berlaku sebelum worker mengambil job.

`make worker` menjalankan proses terpisah. Worker mengambil job dengan `FOR UPDATE SKIP LOCKED`, termasuk job `running` yang `claimed_at`-nya lebih lama dari 30 detik (worker pemegangnya mati). Satu database hanya dikunci satu execution. Lease 30 detik diperpanjang tiap 10 detik. Database lain boleh jalan bersamaan.

Sebelum SQL dijalankan, worker memeriksa lagi: status masih menunggu, hash SQL sama dengan yang disetujui, approval lengkap untuk grant saat ini, peminta masih punya akses edit, dan aturan rantai masih berlaku. Approval yang tidak lengkap mengembalikan execution ke `pending_review` tanpa menjalankan SQL. Alasan lain menandai execution `failed`.

Mode normal menjalankan SQL up atau down di satu transaction target bersama bukti `_dbmp.executions` dan `_dbmp.applied_migrations`. Commit platform hanya dilakukan setelah commit target. Bila hasil commit tidak dapat dipastikan, status menjadi `uncertain`, kunci tetap dipegang, dan job diperiksa lagi nanti: ada bukti berarti sudah commit, tidak ada bukti dan koneksi sehat berarti aman diulang, koneksi mati tetap `uncertain`.

Kegagalan sementara (SQLSTATE kelas `08`, `40001`, `40P01`, `57P01`, `53300`) dicoba lagi paling banyak 3 kali, dengan jeda 5 detik lalu 15 detik. Kesalahan SQL lain membuat job `dead`. Mode batch dijalankan oleh `internal/batch` melalui `migration.Executor`. Status untuk polling ada di `GET /api/jobs/{id}`.

## Dataset besar

`make seed` dan `make dataset` mengisi tabel `big_events` (`id` bigint primary key, `payload` int) di target-db-1. `id` unik dan tidak berubah, jadi mode batch dapat memakai `WHERE id > :from AND id <= :to`. Jumlah baris tetap 400.000. Ulangi pencarian dengan `go run ./cmd/dataset -search`.

Pengukuran pada AMD Ryzen 7 7435HS (8 core / 16 thread), RAM 15,7 GB, Postgres 16 di Docker:

| Cara | Hasil |
|------|--------|
| Satu `UPDATE big_events SET payload = payload + 1` tanpa batch | 1,311 detik |
| Update yang sama, 8 batch × 50.000 baris (`WHERE id > :from AND id <= :to`) | 1,533 detik |

## Stress test k6

Skrip `load/k6/api.js` menaikkan lalu menurunkan beban pada API baca. Tiap VU masuk sebagai `alice` sekali (`GET /api/csrf` lalu `POST /api/auth/login`), kemudian berulang membaca `GET /api/me`, daftar database, daftar migration, dan riwayat execution `target-db-1`. Jeda antar putaran 1 detik. Enqueue dan eksekusi SQL tidak ikut, supaya pengukuran tidak mengisi riwayat dan tidak berebut kunci database. Worker tidak perlu jalan.

Pasang k6 di Windows dengan `winget install --id GrafanaLabs.k6`. Atau lewat Docker, dari akar repo:

```bash
docker run --rm -v "$(pwd)/load/k6:/scripts" -e BASE_URL=http://host.docker.internal:8080 grafana/k6 run /scripts/api.js
```

Jalankan setelah `make up`, `make seed`, dan `make run`:

```bash
make k6
```

`BASE_URL` default `http://localhost:8080`. Lima tingkat berjalan berurutan. Masing-masing naik ke target dalam 15 detik, lalu bertahan 45 detik.

- `5-up`, lalu `15-up`, lalu `30-up`
- turun ke `15-down`, lalu `5-down`

Ambang lulus, untuk seluruh jalan dan untuk tiap tingkat: error rate di bawah 1%, dan p95 request baca di bawah 1 detik.

Pengukuran pada mesin yang sama dengan dataset (AMD Ryzen 7 7435HS, 8 core / 16 thread, RAM 15,7 GB), server Go dan Postgres 16 di mesin lokal:

| Tingkat | VU | Request | p95 baca | Error rate |
|---------|----|---------|----------|------------|
| 5-up | 5 | 770 | 7,11 ms | 0% |
| 15-up | 15 | 2.349 | 7,28 ms | 0% |
| 30-up | 30 | 4.695 | 7,16 ms | 0% |
| 15-down | 15 | 2.339 | 7,12 ms | 0% |
| 5-down | 5 | 764 | 7,51 ms | 0% |

p95 baca seluruh jalan 7,18 ms, error rate 0%. Semua tingkat lulus ambang.

## Analisis EXPLAIN

Tombol **Analisis** di halaman detail migration (draft atau gagal) menjalankan `EXPLAIN` terhadap SQL migration sebelum dieksekusi. Centang "Dengan ANALYZE" untuk `EXPLAIN ANALYZE`.

Cara kerja:

1. SQL divalidasi dengan aturan yang sama seperti saat menyimpan migration (D13), lalu dipecah per statement.
2. Semua statement dijalankan berurutan di **satu transaction yang selalu di-rollback**, dengan `statement_timeout` 30 detik dan `lock_timeout` 5 detik.
3. Statement DML (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `WITH`, `VALUES`) di-`EXPLAIN (FORMAT JSON)`, atau `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` bila ANALYZE dicentang.
4. Statement DDL tidak punya execution plan. Statement itu dijalankan sementara supaya statement berikutnya bisa direncanakan (misalnya `CREATE TABLE t` lalu `INSERT INTO t`).
5. Setiap statement dibungkus `SAVEPOINT`, sehingga error satu statement ditampilkan pada statement itu dan statement lain tetap dianalisis.

Yang ditampilkan per statement: jenis scan dan index yang dipakai, join strategy, estimated cost, estimasi jumlah baris, serta (dengan ANALYZE) waktu aktual, jumlah baris aktual, loops, dan waktu eksekusi. Plan ditampilkan sebagai pohon node.

Penanda pada node (hanya dengan ANALYZE):

- **lambat**: waktu eksklusif node (tanpa anak, dikali loops) ≥ 30% dari waktu eksekusi statement dan minimal 1 ms;
- **estimasi meleset**: jumlah baris aktual berbeda ≥ 10× dari estimasi planner (lebih besar maupun lebih kecil).

Batasan: `nextval()` pada sequence tidak ikut di-rollback, dan `EXPLAIN ANALYZE` memegang lock selama berjalan. Karena itu analisis membutuhkan akses edit. Status migration tidak berubah dan analisis tidak dicatat sebagai percobaan eksekusi.

## Pengujian

- **Unit test** (`make test`) menguji setiap modul secara terisolasi dengan stub/mock: policy dan akses efektif, keputusan rotasi/idle/absolute dengan jam palsu, CSRF, validasi, SQL splitter, enkripsi kredensial, dan handler.
- **Integration test** (`make test-integration`, build tag `integration`) memakai Postgres sungguhan. Setiap test membuat database `test_<acak>` sendiri lewat `testutil.NewPlatformDB` / `NewTargetDB` dan menghapusnya setelah selesai.
  - `cmd/server`: alur minimum di atas dijalankan dari awal sampai akhir lewat router produksi dan template HTML asli. Login, membuat koneksi, group, dan grant, lalu melihat schema/data, membuat dan menjalankan migration (commit dan rollback), memeriksa riwayat, mencabut akses, dan logout.
  - Modul lain menguji kasus berhasil, gagal, dan ditolak untuk session (grace, absolute, idle, rotasi paralel), login dan CSRF, user/group, koneksi dan grant, explorer (paginasi, table tidak ada, nama table berisi SQL injection, target mati), migration, serta analisis EXPLAIN (filter ber-index menghasilkan Index Scan, tanpa index menghasilkan Seq Scan; `CREATE TABLE` + `INSERT` berhasil dianalisis dan table tidak tersisa; error per statement; user view ditolak).

## Struktur

```
cmd/server          wiring aplikasi (app.go) dan entry point
cmd/seed            data demo
internal/auth       login, password, bootstrap admin
internal/session    session: idle, absolute, rotasi, grace
internal/csrf       proteksi CSRF
internal/authz      policy dan akses efektif
internal/admin      user, group (termasuk leader), koneksi database, grant akses
internal/approval   approval perubahan oleh leader group
internal/api        endpoint JSON (/api) untuk auth, admin, dan approval
internal/logx       log JSON terstruktur, redaksi, correlation ID, audit
internal/spa        penyaji hasil build frontend
internal/telemetry  OpenTelemetry tracing, propagasi queue (Inject/Extract), HTTPMiddleware, PgxTracer
internal/batch      eksekusi migration per batch, checkpoint target (_dbmp.batch_checkpoints), resume & revert terbalik
frontend            SPA (React + Vite + TypeScript)
internal/dbconn     koneksi, enkripsi kredensial, pool
internal/explorer   API JSON viewer schema dan data
internal/migration  editor, eksekusi, riwayat
internal/explain    API JSON analisis EXPLAIN / EXPLAIN ANALYZE
internal/config platformdb apperr clock httpx view testutil
db/platform_migrations
web/dist (hasil build SPA) web/templates web/static (halaman HTML lama)
```

## Pembagian kerja

| NIM      | Nama                     | Tanggung jawab |
|----------|--------------------------|----------------|
| 13524014 | Yusuf Faishal Listyardi  | Identitas dan keamanan: login, session, CSRF, authorization (policy dan akses efektif), serta pengelolaan user dan group. |
| 13524002 | Daniel Anindito Nugroho  | Pengelolaan koneksi database, grant akses group ke database, viewer schema dan data, serta engine analisis EXPLAIN. |
| 13524011 | Muhammad Iqbal Raihan    | Infrastruktur platform, template bersama, helper test, serta migration (termasuk SQL splitter dan tombol Analisis). |

## Di luar cakupan

Dari spesifikasi bonus, yang diimplementasikan adalah analisis EXPLAIN. Dukungan MySQL/MariaDB serta schema diff dan migration generation belum diimplementasikan. Untuk MySQL/MariaDB, DDL melakukan implicit commit sehingga jaminan rollback migration tidak bisa dipenuhi.
