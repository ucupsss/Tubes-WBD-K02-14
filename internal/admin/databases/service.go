package databases

import (
	"context"
	"fmt"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

type Service struct {
	db        platformdb.DBTX
	repo      Repo
	manager   dbconn.Manager
	secretKey []byte
}

func NewService(db platformdb.DBTX, repo Repo, mgr dbconn.Manager, secretKey []byte) *Service {
	return &Service{
		db:        db,
		repo:      repo,
		manager:   mgr,
		secretKey: secretKey,
	}
}

func (s *Service) List(ctx context.Context) ([]Connection, error) {
	return s.repo.List(ctx, s.db)
}

func (s *Service) GetByID(ctx context.Context, id int64) (Connection, error) {
	return s.repo.GetByID(ctx, s.db, id)
}

func (s *Service) Create(ctx context.Context, in CreateInput) (int64, error) {
	if errs := in.Validate(); len(errs) > 0 {
		return 0, apperr.InvalidFields("Input tidak valid", errs)
	}

	encPassword, err := dbconn.Encrypt([]byte(in.Password), s.secretKey)
	if err != nil {
		return 0, fmt.Errorf("gagal mengenkripsi password: %w", err)
	}

	conn := Connection{
		Name:        strings.TrimSpace(in.Name),
		Host:        strings.TrimSpace(in.Host),
		Port:        in.Port,
		DBName:      strings.TrimSpace(in.DBName),
		Username:    strings.TrimSpace(in.Username),
		PasswordEnc: encPassword,
		SSLMode:     in.SSLMode,
		CreatedBy:   &in.CreatedBy,
	}

	return s.repo.Create(ctx, s.db, conn)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) error {
	if errs := in.Validate(); len(errs) > 0 {
		return apperr.InvalidFields("Input tidak valid", errs)
	}

	existing, err := s.repo.GetByID(ctx, s.db, id)
	if err != nil {
		return err
	}

	encPassword := existing.PasswordEnc
	// Jika password diisi, enkripsi password baru
	if strings.TrimSpace(in.Password) != "" {
		newEnc, err := dbconn.Encrypt([]byte(in.Password), s.secretKey)
		if err != nil {
			return fmt.Errorf("gagal mengenkripsi password baru: %w", err)
		}
		encPassword = newEnc
	}

	updated := Connection{
		ID:          id,
		Name:        strings.TrimSpace(in.Name),
		Host:        strings.TrimSpace(in.Host),
		Port:        in.Port,
		DBName:      strings.TrimSpace(in.DBName),
		Username:    strings.TrimSpace(in.Username),
		PasswordEnc: encPassword,
		SSLMode:     in.SSLMode,
	}

	if err := s.repo.Update(ctx, s.db, updated); err != nil {
		return err
	}

	// Invalidasi pool di cache karena konfigurasi/kredensial berubah
	s.manager.Invalidate(id)
	return nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	hasMigrations, err := s.repo.HasMigrations(ctx, s.db, id)
	if err != nil {
		return err
	}
	if hasMigrations {
		return ErrHasMigrations
	}

	if err := s.repo.Delete(ctx, s.db, id); err != nil {
		return err
	}

	s.manager.Invalidate(id)
	return nil
}

func (s *Service) TestConnection(ctx context.Context, id int64, in UpdateInput) error {
	plainPassword := in.Password
	conn := Connection{
		Host:     strings.TrimSpace(in.Host),
		Port:     in.Port,
		DBName:   strings.TrimSpace(in.DBName),
		Username: strings.TrimSpace(in.Username),
		SSLMode:  in.SSLMode,
	}

	// Jika password kosong dan ID ada, gunakan password tersimpan DAN kunci Host/Port/Username/SSLMode
	// ke nilai tersimpan agar password write-only tidak dapat dieksfiltrasi ke host lain.
	if plainPassword == "" && id > 0 {
		existing, err := s.repo.GetByID(ctx, s.db, id)
		if err != nil {
			return err
		}
		decrypted, err := dbconn.Decrypt(existing.PasswordEnc, s.secretKey)
		if err != nil {
			return fmt.Errorf("gagal mendekripsi password yang tersimpan: %w", err)
		}
		plainPassword = string(decrypted)
		conn.Host = existing.Host
		conn.Port = existing.Port
		conn.Username = existing.Username
		conn.SSLMode = existing.SSLMode
		if conn.DBName == "" {
			conn.DBName = existing.DBName
		}
	}

	if plainPassword == "" {
		return apperr.InvalidFields("Input tidak valid", map[string]string{
			"password": "Password wajib diisi untuk menguji koneksi",
		})
	}

	return s.manager.TestConnection(ctx, conn, plainPassword)
}
