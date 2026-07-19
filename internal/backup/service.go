package backup

import (
	"archive/zip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/argon2"
)

const (
	FormatVersion       = 1
	MinPassphraseLength = 12
	MaxPassphraseLength = 1024
	maxDatabaseBytes    = 4 << 30
	MaxUploadBytes      = maxDatabaseBytes + (64 << 20)
	chunkSize           = 1 << 20
	magic               = "FAROBKP1"
)

var ErrInvalidBackup = errors.New("backup is invalid, corrupted, or protected by a different passphrase")

var restoreTables = []string{
	"settings",
	"dns_records",
	"blocklists",
	"blocklist_entries",
	"protection_profiles",
	"protection_blocklists",
	"protection_allow_entries",
	"protection_block_entries",
	"device_protection_assignments",
	"allowlist_entries",
	"manual_block_entries",
	"dns_queries",
	"device_aliases",
	"events",
	"domain_favicons",
	"users",
	"notification_states",
}

var deleteTables = []string{
	"notification_states",
	"auth_sessions",
	"device_protection_assignments",
	"protection_block_entries",
	"protection_allow_entries",
	"protection_blocklists",
	"blocklist_entries",
	"domain_favicons",
	"events",
	"device_aliases",
	"dns_queries",
	"manual_block_entries",
	"allowlist_entries",
	"protection_profiles",
	"blocklists",
	"dns_records",
	"settings",
	"users",
}

type Manifest struct {
	FormatVersion int      `json:"format_version"`
	CreatedAt     string   `json:"created_at"`
	DatabaseBytes int64    `json:"database_bytes"`
	Excluded      []string `json:"excluded"`
}

type Service struct {
	store *db.Store
	mu    sync.Mutex
}

func NewService(store *db.Store) *Service {
	return &Service{store: store}
}

// Create writes a consistent, encrypted backup to a temporary file. The caller
// owns the returned cleanup function.
func (s *Service) Create(ctx context.Context, passphrase string) (string, Manifest, func(), error) {
	if err := validatePassphrase(passphrase); err != nil {
		return "", Manifest{}, func() {}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tempDir, err := os.MkdirTemp("", "faro-backup-")
	if err != nil {
		return "", Manifest{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	databasePath := filepath.Join(tempDir, "faro.db")
	if err := snapshotDatabase(ctx, s.store.DB, databasePath); err != nil {
		cleanup()
		return "", Manifest{}, func() {}, fmt.Errorf("snapshot database: %w", err)
	}
	if err := scrubSnapshot(databasePath); err != nil {
		cleanup()
		return "", Manifest{}, func() {}, fmt.Errorf("prepare snapshot: %w", err)
	}
	info, err := os.Stat(databasePath)
	if err != nil {
		cleanup()
		return "", Manifest{}, func() {}, err
	}
	if info.Size() > maxDatabaseBytes {
		cleanup()
		return "", Manifest{}, func() {}, errors.New("database is too large for a portable Faro backup")
	}
	manifest := Manifest{
		FormatVersion: FormatVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		DatabaseBytes: info.Size(),
		Excluded:      []string{"auth_sessions", "favicon cache files", "raw query-log buffer"},
	}
	archivePath := filepath.Join(tempDir, "payload.zip")
	if err := writeArchive(archivePath, databasePath, manifest); err != nil {
		cleanup()
		return "", Manifest{}, func() {}, err
	}
	encryptedPath := filepath.Join(tempDir, "faro-backup.faro-backup")
	if err := encryptFile(archivePath, encryptedPath, passphrase); err != nil {
		cleanup()
		return "", Manifest{}, func() {}, err
	}
	_ = os.Remove(archivePath)
	_ = os.Remove(databasePath)
	return encryptedPath, manifest, cleanup, nil
}

func (s *Service) Restore(ctx context.Context, encrypted io.Reader, passphrase string) (Manifest, error) {
	if err := validatePassphrase(passphrase); err != nil {
		return Manifest{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tempDir, err := os.MkdirTemp("", "faro-restore-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, "payload.zip")
	if err := decryptToFile(encrypted, archivePath, passphrase); err != nil {
		return Manifest{}, ErrInvalidBackup
	}
	databasePath := filepath.Join(tempDir, "faro.db")
	manifest, err := extractArchive(archivePath, databasePath)
	if err != nil {
		return Manifest{}, ErrInvalidBackup
	}
	if manifest.FormatVersion != FormatVersion {
		return Manifest{}, fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	if err := prepareRestoreDatabase(databasePath); err != nil {
		return Manifest{}, fmt.Errorf("validate backup database: %w", ErrInvalidBackup)
	}
	if err := restoreDatabase(ctx, s.store.DB, databasePath); err != nil {
		return Manifest{}, fmt.Errorf("restore database: %w", err)
	}
	return manifest, nil
}

func validatePassphrase(passphrase string) error {
	if len(passphrase) < MinPassphraseLength {
		return fmt.Errorf("backup passphrase must be at least %d characters", MinPassphraseLength)
	}
	if len(passphrase) > MaxPassphraseLength {
		return errors.New("backup passphrase is too long")
	}
	return nil
}

func snapshotDatabase(ctx context.Context, source *sql.DB, destinationPath string) error {
	destination, err := sql.Open("sqlite3", destinationPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer destination.Close()
	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer sourceConn.Close()
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer destinationConn.Close()
	return sourceConn.Raw(func(sourceDriver any) error {
		src, ok := sourceDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("unexpected SQLite source connection")
		}
		return destinationConn.Raw(func(destinationDriver any) error {
			dst, ok := destinationDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("unexpected SQLite destination connection")
			}
			backup, err := dst.Backup("main", src, "main")
			if err != nil {
				return err
			}
			done, stepErr := backup.Step(-1)
			closeErr := backup.Finish()
			if stepErr != nil {
				return stepErr
			}
			if closeErr != nil {
				return closeErr
			}
			if !done {
				return errors.New("SQLite backup did not complete")
			}
			return nil
		})
	})
}

func scrubSnapshot(path string) error {
	database, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM auth_sessions`); err != nil {
		return err
	}
	return integrityCheck(database)
}

func writeArchive(archivePath, databasePath string, manifest Manifest) error {
	output, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	manifestWriter, err := archive.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if err == nil {
		err = json.NewEncoder(manifestWriter).Encode(manifest)
	}
	if err == nil {
		var databaseWriter io.Writer
		databaseWriter, err = archive.CreateHeader(&zip.FileHeader{Name: "faro.db", Method: zip.Deflate})
		if err == nil {
			var database *os.File
			database, err = os.Open(databasePath)
			if err == nil {
				_, err = io.Copy(databaseWriter, database)
				_ = database.Close()
			}
		}
	}
	closeArchiveErr := archive.Close()
	closeFileErr := output.Close()
	if err != nil {
		return err
	}
	if closeArchiveErr != nil {
		return closeArchiveErr
	}
	return closeFileErr
}

func extractArchive(archivePath, databasePath string) (Manifest, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	defer reader.Close()
	var manifest Manifest
	foundManifest := false
	foundDatabase := false
	for _, file := range reader.File {
		switch file.Name {
		case "manifest.json":
			if foundManifest || file.UncompressedSize64 > 64<<10 {
				return Manifest{}, ErrInvalidBackup
			}
			input, openErr := file.Open()
			if openErr != nil {
				return Manifest{}, openErr
			}
			err = json.NewDecoder(io.LimitReader(input, 64<<10)).Decode(&manifest)
			_ = input.Close()
			if err != nil {
				return Manifest{}, err
			}
			foundManifest = true
		case "faro.db":
			if foundDatabase || file.UncompressedSize64 > maxDatabaseBytes {
				return Manifest{}, ErrInvalidBackup
			}
			input, openErr := file.Open()
			if openErr != nil {
				return Manifest{}, openErr
			}
			output, createErr := os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if createErr == nil {
				var copied int64
				copied, createErr = io.Copy(output, io.LimitReader(input, maxDatabaseBytes+1))
				if copied > maxDatabaseBytes {
					createErr = ErrInvalidBackup
				}
			}
			if output != nil {
				_ = output.Close()
			}
			_ = input.Close()
			if createErr != nil {
				return Manifest{}, createErr
			}
			foundDatabase = true
		}
	}
	if !foundManifest || !foundDatabase {
		return Manifest{}, ErrInvalidBackup
	}
	info, err := os.Stat(databasePath)
	if err != nil || info.Size() != manifest.DatabaseBytes {
		return Manifest{}, ErrInvalidBackup
	}
	return manifest, nil
}

func prepareRestoreDatabase(path string) error {
	store, err := db.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	return integrityCheck(store.DB)
}

func integrityCheck(database *sql.DB) error {
	var result string
	if err := database.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	return nil
}

func restoreDatabase(ctx context.Context, live *sql.DB, sourcePath string) error {
	connection, err := live.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `ATTACH DATABASE ? AS restore_source`, sourcePath); err != nil {
		return err
	}
	defer connection.ExecContext(context.Background(), `DETACH DATABASE restore_source`)
	for _, table := range restoreTables {
		var sourceType string
		if err := connection.QueryRowContext(ctx, `SELECT type FROM restore_source.sqlite_master WHERE name = ?`, table).Scan(&sourceType); err != nil || sourceType != "table" {
			return fmt.Errorf("backup table %s is missing or invalid", table)
		}
		compatible, err := compatibleColumns(ctx, connection, table)
		if err != nil {
			return err
		}
		if !compatible {
			return fmt.Errorf("backup table %s is incompatible with this Faro version", table)
		}
	}
	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range deleteTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	for _, table := range restoreTables {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` SELECT * FROM restore_source.`+table); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("restored database violates relational integrity")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func compatibleColumns(ctx context.Context, connection *sql.Conn, table string) (bool, error) {
	mainColumns, err := tableColumns(ctx, connection, "main", table)
	if err != nil {
		return false, err
	}
	restoreColumns, err := tableColumns(ctx, connection, "restore_source", table)
	if err != nil {
		return false, err
	}
	return strings.Join(mainColumns, "\x00") == strings.Join(restoreColumns, "\x00") && len(mainColumns) > 0, nil
}

func tableColumns(ctx context.Context, connection *sql.Conn, schema, table string) ([]string, error) {
	rows, err := connection.QueryContext(ctx, `PRAGMA `+schema+`.table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func encryptFile(sourcePath, destinationPath, passphrase string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = encrypt(destination, source, passphrase)
	closeErr := destination.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func encrypt(destination io.Writer, source io.Reader, passphrase string) error {
	salt := make([]byte, 16)
	noncePrefix := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(noncePrefix); err != nil {
		return err
	}
	header := append(append(append([]byte{}, []byte(magic)...), byte(FormatVersion)), salt...)
	header = append(header, noncePrefix...)
	if _, err := destination.Write(header); err != nil {
		return err
	}
	aead, err := newAEAD(passphrase, salt)
	if err != nil {
		return err
	}
	buffer := make([]byte, chunkSize)
	for counter := uint32(0); ; counter++ {
		read, readErr := io.ReadFull(source, buffer)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return readErr
		}
		final := readErr == io.ErrUnexpectedEOF || readErr == io.EOF
		plain := make([]byte, read+1)
		if final {
			plain[0] = 1
		}
		copy(plain[1:], buffer[:read])
		sealed := aead.Seal(nil, nonce(noncePrefix, counter), plain, additionalData(counter))
		if err := binary.Write(destination, binary.BigEndian, uint32(len(sealed))); err != nil {
			return err
		}
		if _, err := destination.Write(sealed); err != nil {
			return err
		}
		if final {
			return nil
		}
	}
}

func decryptToFile(source io.Reader, destinationPath, passphrase string) error {
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = decrypt(destination, source, passphrase)
	closeErr := destination.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func decrypt(destination io.Writer, source io.Reader, passphrase string) error {
	header := make([]byte, len(magic)+1+16+8)
	if _, err := io.ReadFull(source, header); err != nil {
		return ErrInvalidBackup
	}
	if string(header[:len(magic)]) != magic || int(header[len(magic)]) != FormatVersion {
		return ErrInvalidBackup
	}
	salt := header[len(magic)+1 : len(magic)+1+16]
	noncePrefix := header[len(magic)+1+16:]
	aead, err := newAEAD(passphrase, salt)
	if err != nil {
		return err
	}
	for counter := uint32(0); ; counter++ {
		var length uint32
		if err := binary.Read(source, binary.BigEndian, &length); err != nil {
			return ErrInvalidBackup
		}
		if length < uint32(aead.Overhead()+1) || length > uint32(chunkSize+1+aead.Overhead()) {
			return ErrInvalidBackup
		}
		sealed := make([]byte, length)
		if _, err := io.ReadFull(source, sealed); err != nil {
			return ErrInvalidBackup
		}
		plain, err := aead.Open(nil, nonce(noncePrefix, counter), sealed, additionalData(counter))
		if err != nil || len(plain) == 0 || plain[0] > 1 {
			return ErrInvalidBackup
		}
		if _, err := destination.Write(plain[1:]); err != nil {
			return err
		}
		if plain[0] == 1 {
			var trailing [1]byte
			if read, trailingErr := source.Read(trailing[:]); read != 0 || (trailingErr != nil && trailingErr != io.EOF) {
				return ErrInvalidBackup
			}
			return nil
		}
	}
}

func newAEAD(passphrase string, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(prefix []byte, counter uint32) []byte {
	value := make([]byte, 12)
	copy(value, prefix)
	binary.BigEndian.PutUint32(value[8:], counter)
	return value
}

func additionalData(counter uint32) []byte {
	value := make([]byte, len(magic)+1+4)
	copy(value, magic)
	value[len(magic)] = byte(FormatVersion)
	binary.BigEndian.PutUint32(value[len(magic)+1:], counter)
	return value
}
