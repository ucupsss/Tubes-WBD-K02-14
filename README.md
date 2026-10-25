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

Butuh Go 1.24+, Docker Desktop, dan `make` (Git Bash). Pastikan port di bawah tidak sedang dipakai Postgres lokal atau container lain.

```bash
cp .env.example .env
# isi APP_SECRET_KEY (openssl rand -base64 32) dan BOOTSTRAP_ADMIN_PASSWORD
make up
make run          # http://localhost:8080
make seed         # user, group, koneksi, dan grant untuk demo
```

`make run` menjalankan migrasi platform secara otomatis. Jika ada file migrasi baru yang nomornya lebih kecil dari yang sudah diterapkan, jalankan `make db-reset`.

| Perintah                | Fungsi                                                                    |
|-------------------------|---------------------------------------------------------------------------|
| `make up` / `make down` | Menyalakan / mematikan Postgres                                           |
| `make db-reset`         | Menghapus volume lalu menyalakan ulang dari kosong                        |
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

## Migration

Riwayat migration linear per database: versi 1, 2, 3, ... tanpa celah. Versi baru hanya ditulis bila seluruh script berhasil di-commit.

Langkah eksekusi:

1. Mengunci baris koneksi di platform database.
2. Menolak percobaan bila status bukan `draft` atau `failed`, atau bila SQL melanggar D13.
3. Menjalankan script di dalam satu transaction pada database target, dengan `statement_timeout` 60 detik dan `lock_timeout` 5 detik. Simple query protocol dipakai supaya satu field bisa berisi beberapa statement.
4. Di transaction yang sama, mencatat versi di tabel `_dbmp.applied_migrations` pada database target.
5. Commit di target, lalu menulis versi, status, pelaksana, waktu, dan satu baris `migration_runs` di platform. Bila ada statement yang gagal, transaction target di-rollback (statement sebelumnya ikut hilang) dan status menjadi `failed`.

Bila proses mati sebelum commit di platform, status tetap `draft` atau `failed` karena perubahan ke `running` tidak pernah di-commit. Bila target sudah commit tetapi update platform gagal, baris di `_dbmp.applied_migrations` tetap ada dan bisa dipakai untuk rekonsiliasi.

Error Postgres dari SQL milik user (misalnya relation sudah ada) ditampilkan apa adanya. Pesan kegagalan koneksi tidak memuat password maupun DSN.

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
internal/admin      user, group, koneksi database, grant akses
internal/dbconn     koneksi, enkripsi kredensial, pool
internal/explorer   viewer schema dan data
internal/migration  editor, eksekusi, riwayat
internal/explain    analisis EXPLAIN / EXPLAIN ANALYZE
internal/config platformdb apperr clock httpx view testutil
db/platform_migrations
web/templates web/static
```

## Pembagian kerja

| NIM      | Nama                     | Tanggung jawab |
|----------|--------------------------|----------------|
| 13524014 | Yusuf Faishal Listyardi  | Identitas dan keamanan: login, session, CSRF, authorization (policy dan akses efektif), serta pengelolaan user dan group. |
| 13524002 | Daniel Anindito Nugroho  | Pengelolaan koneksi database, grant akses group ke database, viewer schema dan data, serta engine analisis EXPLAIN. |
| 13524011 | Muhammad Iqbal Raihan    | Infrastruktur platform, template bersama, helper test, serta migration (termasuk SQL splitter dan tombol Analisis). |

## Di luar cakupan

Dari spesifikasi bonus, yang diimplementasikan adalah analisis EXPLAIN. Dukungan MySQL/MariaDB serta schema diff dan migration generation belum diimplementasikan. Untuk MySQL/MariaDB, DDL melakukan implicit commit sehingga jaminan rollback migration tidak bisa dipenuhi.
