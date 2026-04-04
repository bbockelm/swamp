// Package backup provides automated and manual backup creation, encryption,
// storage, and restore functionality for SWAMP.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
)

// Service manages backup creation, encryption, and storage.
type Service struct {
	cfg              *config.Config
	queries          *db.Queries
	store            *storage.Store
	enc              *crypto.Encryptor
	masterKeyHex     string
	generalBackupKey []byte
}

// NewService creates a backup service.
func NewService(cfg *config.Config, queries *db.Queries, store *storage.Store, enc *crypto.Encryptor) *Service {
	s := &Service{cfg: cfg, queries: queries, store: store, enc: enc}
	if cfg.InstanceKey != "" {
		s.masterKeyHex = cfg.InstanceKey
		if gbk, err := crypto.DeriveBackupKey(cfg.InstanceKey); err == nil {
			s.generalBackupKey = gbk
		}
	}
	return s
}

// GeneralBackupKeyHex returns the hex-encoded general backup key.
func (s *Service) GeneralBackupKeyHex() (string, error) {
	if len(s.generalBackupKey) == 0 {
		return "", fmt.Errorf("encryption not configured")
	}
	return hex.EncodeToString(s.generalBackupKey), nil
}

// PerBackupKeyHex returns the hex-encoded per-backup decryption key.
func (s *Service) PerBackupKeyHex(backupFilename string) (string, error) {
	if len(s.generalBackupKey) == 0 {
		return "", fmt.Errorf("encryption not configured")
	}
	pbk, err := crypto.DerivePerBackupKey(s.generalBackupKey, backupFilename)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(pbk), nil
}

const backupS3Prefix = "backups/"

// s3Entry is a key/data pair for an S3 object extracted from a backup tarball.
type s3Entry struct {
	key  string
	data []byte
}

// GetSettings reads backup settings from app_config.
func (s *Service) GetSettings(ctx context.Context) models.BackupSettings {
	freq, _ := s.queries.GetAppConfig(ctx, "backup_frequency_hours")
	bucket, _ := s.queries.GetAppConfig(ctx, "backup_bucket")
	endpoint, _ := s.queries.GetAppConfig(ctx, "backup_endpoint")
	accessKey, _ := s.queries.GetAppConfig(ctx, "backup_access_key")
	secretKey, _ := s.queries.GetAppConfig(ctx, "backup_secret_key")
	useSSL, _ := s.queries.GetAppConfig(ctx, "backup_use_ssl")

	if s.enc != nil {
		if dec, err := s.enc.DecryptConfigValue(accessKey); err == nil {
			accessKey = dec
		}
		if dec, err := s.enc.DecryptConfigValue(secretKey); err == nil {
			secretKey = dec
		}
	}

	hours := 0
	if freq != "" {
		fmt.Sscanf(freq, "%d", &hours)
	}
	return models.BackupSettings{
		BackupFrequencyHours: hours,
		BackupBucket:         bucket,
		BackupEndpoint:       endpoint,
		BackupAccessKey:      accessKey,
		BackupSecretKey:      secretKey,
		BackupUseSSL:         useSSL == "true",
	}
}

// SaveSettings persists backup settings to app_config.
func (s *Service) SaveSettings(ctx context.Context, settings models.BackupSettings) error {
	accessKey := settings.BackupAccessKey
	secretKey := settings.BackupSecretKey
	if s.enc != nil {
		if accessKey != "" {
			if enc, err := s.enc.EncryptConfigValue(accessKey); err == nil {
				accessKey = enc
			}
		}
		if secretKey != "" {
			if enc, err := s.enc.EncryptConfigValue(secretKey); err == nil {
				secretKey = enc
			}
		}
	}

	pairs := map[string]string{
		"backup_frequency_hours": fmt.Sprintf("%d", settings.BackupFrequencyHours),
		"backup_bucket":          settings.BackupBucket,
		"backup_endpoint":        settings.BackupEndpoint,
		"backup_access_key":      accessKey,
		"backup_secret_key":      secretKey,
		"backup_use_ssl":         fmt.Sprintf("%t", settings.BackupUseSSL),
	}
	for k, v := range pairs {
		if err := s.queries.SetAppConfig(ctx, k, v); err != nil {
			return fmt.Errorf("setting %s: %w", k, err)
		}
	}
	return nil
}

func (s *Service) backupClient(ctx context.Context) (*s3.Client, string, error) {
	settings := s.GetSettings(ctx)

	if settings.BackupEndpoint != "" && settings.BackupBucket != "" {
		accessKey := settings.BackupAccessKey
		secretKey := settings.BackupSecretKey
		if accessKey == "" {
			accessKey = s.cfg.S3AccessKey
		}
		if secretKey == "" {
			secretKey = s.cfg.S3SecretKey
		}

		client := storage.NewS3Client(settings.BackupEndpoint, accessKey, secretKey, settings.BackupUseSSL, true)

		_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
			Bucket: aws.String(settings.BackupBucket),
		})
		if err != nil {
			_, createErr := client.CreateBucket(ctx, &s3.CreateBucketInput{
				Bucket: aws.String(settings.BackupBucket),
			})
			if createErr != nil {
				return nil, "", fmt.Errorf("creating backup bucket: %w", createErr)
			}
		}
		return client, settings.BackupBucket, nil
	}

	bucket := settings.BackupBucket
	if bucket == "" {
		bucket = s.store.Bucket()
	}
	return s.store.Client(), bucket, nil
}

// StartBackup creates a backup DB record and returns it.
func (s *Service) StartBackup(ctx context.Context, initiatedBy string) (*models.Backup, error) {
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("swamp-backup-%s.tar.gz.enc", timestamp)
	s3Key := backupS3Prefix + filename

	_, bucket, err := s.backupClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting backup client: %w", err)
	}

	backup := &models.Backup{
		Filename:    filename,
		S3Key:       s3Key,
		S3Bucket:    bucket,
		Status:      "running",
		InitiatedBy: initiatedBy,
		Encrypted:   len(s.generalBackupKey) > 0,
	}
	if err := s.queries.CreateBackup(ctx, backup); err != nil {
		return nil, fmt.Errorf("creating backup record: %w", err)
	}
	return backup, nil
}

// RunBackup performs the actual backup in a goroutine-safe manner.
func (s *Service) RunBackup(ctx context.Context, backup *models.Backup) {
	startTime := time.Now()
	logger := log.With().Str("backup_id", backup.ID).Logger()
	logger.Info().Msg("Starting backup")

	var buf bytes.Buffer
	if err := s.createTarball(ctx, &buf); err != nil {
		logger.Error().Err(err).Msg("Failed to create tarball")
		s.failBackup(ctx, backup, err.Error())
		return
	}

	plainSize := int64(buf.Len())
	hashBytes := sha256.Sum256(buf.Bytes())
	plainHash := hex.EncodeToString(hashBytes[:])

	// Encrypt if possible
	var encData []byte
	if pbk, err := s.PerBackupKeyHex(backup.Filename); err == nil {
		pbkBytes, _ := hex.DecodeString(pbk)
		dek, _ := crypto.GenerateDEK()
		wrappedDEK, wrappedNonce, _ := crypto.WrapDEKWithKey(pbkBytes, dek)

		encrypted, err := crypto.Encrypt(dek, buf.Bytes())
		if err != nil {
			s.failBackup(ctx, backup, "encryption failed: "+err.Error())
			return
		}
		// Format: wrappedDEK (hex) + ":" + nonce (hex) + "\n" + encrypted data
		header := hex.EncodeToString(wrappedDEK) + ":" + hex.EncodeToString(wrappedNonce) + "\n"
		encData = append([]byte(header), encrypted...)
	} else {
		encData = buf.Bytes()
	}

	// Upload to S3
	client, bucket, err := s.backupClient(ctx)
	if err != nil {
		s.failBackup(ctx, backup, "backup client: "+err.Error())
		return
	}

	reader := bytes.NewReader(encData)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(backup.S3Key),
		Body:          reader,
		ContentLength: aws.Int64(int64(len(encData))),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		s.failBackup(ctx, backup, "S3 upload: "+err.Error())
		return
	}

	now := time.Now()
	backup.Status = "completed"
	backup.SizeBytes = int64(len(encData))
	backup.Checksum = plainHash
	backup.DurationSecs = int(now.Sub(startTime).Seconds())
	backup.CompletedAt = &now
	_ = s.queries.UpdateBackup(ctx, backup)

	logger.Info().
		Int64("plain_size", plainSize).
		Int64("enc_size", backup.SizeBytes).
		Int("duration_secs", backup.DurationSecs).
		Msg("Backup completed")
}

func (s *Service) failBackup(ctx context.Context, backup *models.Backup, errMsg string) {
	backup.Status = "failed"
	backup.ErrorMsg = errMsg
	_ = s.queries.UpdateBackup(ctx, backup)
}

func (s *Service) createTarball(ctx context.Context, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Build a map of analysisID → DEK for decrypting S3 objects.
	// This lets the backup contain plaintext data so it can be restored
	// with only the per-backup key, without needing the instance master key.
	dekCache := make(map[string][]byte) // analysisID → plaintext DEK (nil = unencrypted)

	// Dump database
	dbDump, err := s.dumpDatabase()
	if err != nil {
		return fmt.Errorf("dumping database: %w", err)
	}
	// Strip per-analysis encryption keys from the dump since the backup
	// will contain decrypted S3 objects. This makes the backup fully
	// restorable with only the per-backup key.
	dbDump = nullifyAnalysisDEKs(dbDump)
	// Decrypt enc:v1: config values so the backup is portable across instances.
	if s.enc != nil {
		dbDump = decryptConfigValues(dbDump, s.enc)
	}
	if err := addToTar(tw, "database.sql", dbDump); err != nil {
		return fmt.Errorf("adding database dump: %w", err)
	}

	// Add S3 objects (analysis artifacts), decrypting them so the backup
	// is self-contained.
	keys, err := s.store.ListKeys(ctx, "")
	if err != nil {
		return fmt.Errorf("listing S3 objects: %w", err)
	}
	for _, key := range keys {
		if strings.HasPrefix(key, backupS3Prefix) {
			continue // Don't backup backups
		}
		obj, err := s.store.Download(ctx, key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Skipping object in backup")
			continue
		}
		data, err := io.ReadAll(obj)
		obj.Close()
		if err != nil {
			continue
		}

		// Attempt to decrypt if this is an analysis artifact.
		if s.enc != nil && strings.HasPrefix(key, "analyses/") {
			if plaintext, err := s.decryptS3Object(ctx, key, data, dekCache); err == nil {
				data = plaintext
			} else {
				log.Warn().Err(err).Str("key", key).Msg("Could not decrypt object, backing up as-is")
			}
		}

		if err := addToTar(tw, "s3/"+key, data); err != nil {
			return fmt.Errorf("adding S3 object %s: %w", key, err)
		}
	}

	return nil
}

// decryptS3Object decrypts an S3 object using the per-analysis DEK.
// It caches DEKs by analysisID to avoid repeated DB lookups and unwrap operations.
func (s *Service) decryptS3Object(ctx context.Context, key string, ciphertext []byte, dekCache map[string][]byte) ([]byte, error) {
	// Extract analysisID from key: analyses/{analysisID}/filename
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected S3 key format: %s", key)
	}
	analysisID := parts[1]

	dek, ok := dekCache[analysisID]
	if !ok {
		analysis, err := s.queries.GetAnalysis(ctx, analysisID)
		if err != nil {
			dekCache[analysisID] = nil
			return nil, fmt.Errorf("loading analysis %s: %w", analysisID, err)
		}
		if len(analysis.EncryptedDEK) == 0 {
			dekCache[analysisID] = nil
			return ciphertext, nil // not encrypted
		}
		dek, err = s.enc.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
		if err != nil {
			dekCache[analysisID] = nil
			return nil, fmt.Errorf("unwrapping DEK for analysis %s: %w", analysisID, err)
		}
		dekCache[analysisID] = dek
	}
	if dek == nil {
		return ciphertext, nil // unencrypted analysis
	}

	plaintext, err := crypto.Decrypt(dek, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypting object %s: %w", key, err)
	}
	return plaintext, nil
}

// nullifyAnalysisDEKs replaces non-null encrypted_dek/dek_nonce values in the
// pg_dump COPY output for the analyses table with \N (SQL NULL). This ensures
// the restored database treats all S3 data as plaintext, since the backup
// already contains decrypted objects.
func nullifyAnalysisDEKs(dump []byte) []byte {
	lines := bytes.Split(dump, []byte("\n"))
	inAnalysesCopy := false
	// Identify the column positions of encrypted_dek and dek_nonce from the
	// COPY header. Format: COPY public.analyses (col1, col2, ...) FROM stdin;
	var dekCol, nonceCol int = -1, -1

	for i, line := range lines {
		lineStr := string(line)

		if !inAnalysesCopy {
			// Look for COPY ... analyses ... FROM stdin;
			if (strings.Contains(lineStr, "COPY public.analyses") || strings.Contains(lineStr, "COPY analyses")) &&
				strings.Contains(lineStr, "FROM stdin") {
				inAnalysesCopy = true
				// Parse column list
				start := strings.Index(lineStr, "(")
				end := strings.LastIndex(lineStr, ")")
				if start >= 0 && end > start {
					cols := strings.Split(lineStr[start+1:end], ",")
					for j, col := range cols {
						col = strings.TrimSpace(col)
						if col == "encrypted_dek" {
							dekCol = j
						} else if col == "dek_nonce" {
							nonceCol = j
						}
					}
				}
			}
			continue
		}

		// Inside COPY block: tab-separated values, end with \. on a line
		if bytes.Equal(line, []byte("\\.")) || bytes.Equal(line, []byte("\\.")) {
			inAnalysesCopy = false
			dekCol, nonceCol = -1, -1
			continue
		}

		if len(line) == 0 {
			continue
		}

		fields := bytes.Split(line, []byte("\t"))
		changed := false
		if dekCol >= 0 && dekCol < len(fields) && !bytes.Equal(fields[dekCol], []byte("\\N")) {
			fields[dekCol] = []byte("\\N")
			changed = true
		}
		if nonceCol >= 0 && nonceCol < len(fields) && !bytes.Equal(fields[nonceCol], []byte("\\N")) {
			fields[nonceCol] = []byte("\\N")
			changed = true
		}
		if changed {
			lines[i] = bytes.Join(fields, []byte("\t"))
		}
	}

	return bytes.Join(lines, []byte("\n"))
}

// decryptConfigValues decrypts enc:v1: values in the app_config COPY block
// of a pg_dump so the backup contains portable plaintext.
func decryptConfigValues(dump []byte, enc *crypto.Encryptor) []byte {
	lines := bytes.Split(dump, []byte("\n"))
	inConfigCopy := false
	var valueCol int = -1

	for i, line := range lines {
		lineStr := string(line)

		if !inConfigCopy {
			if (strings.Contains(lineStr, "COPY public.app_config") || strings.Contains(lineStr, "COPY app_config")) &&
				strings.Contains(lineStr, "FROM stdin") {
				inConfigCopy = true
				start := strings.Index(lineStr, "(")
				end := strings.LastIndex(lineStr, ")")
				if start >= 0 && end > start {
					cols := strings.Split(lineStr[start+1:end], ",")
					for j, col := range cols {
						if strings.TrimSpace(col) == "value" {
							valueCol = j
						}
					}
				}
			}
			continue
		}

		if bytes.Equal(line, []byte("\\.")) {
			inConfigCopy = false
			valueCol = -1
			continue
		}

		if len(line) == 0 || valueCol < 0 {
			continue
		}

		fields := bytes.Split(line, []byte("\t"))
		if valueCol < len(fields) {
			val := string(fields[valueCol])
			if crypto.IsEncryptedConfig(val) {
				if plaintext, err := enc.DecryptConfigValue(val); err == nil {
					fields[valueCol] = []byte(plaintext)
					lines[i] = bytes.Join(fields, []byte("\t"))
				}
			}
		}
	}

	return bytes.Join(lines, []byte("\n"))
}

// sensitiveConfigKeys lists app_config keys whose values are encrypted with
// the instance's configKEK. On restore these are re-encrypted with the new key.
var sensitiveConfigKeys = []string{
	"backup_access_key",
	"backup_secret_key",
	"oidc_client_secret",
}

// reencryptConfigValues re-encrypts known sensitive app_config values with
// this instance's configKEK after a database restore.
func (s *Service) reencryptConfigValues(ctx context.Context) error {
	for _, key := range sensitiveConfigKeys {
		val, err := s.queries.GetAppConfig(ctx, key)
		if err != nil || val == "" {
			continue
		}
		// Already encrypted (e.g. restoring on the same instance) — skip.
		if crypto.IsEncryptedConfig(val) {
			continue
		}
		encrypted, err := s.enc.EncryptConfigValue(val)
		if err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to re-encrypt config value")
			continue
		}
		if err := s.queries.SetAppConfig(ctx, key, encrypted); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to save re-encrypted config value")
		}
	}
	return nil
}

func (s *Service) dumpDatabase() ([]byte, error) {
	cmd := exec.Command("pg_dump", s.cfg.DatabaseURL)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pg_dump: %s: %w", stderr.String(), err)
	}
	return out.Bytes(), nil
}

func addToTar(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0o644,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// DownloadBackup returns a reader for the backup file from S3.
func (s *Service) DownloadBackup(ctx context.Context, backupID string) (io.ReadCloser, *models.Backup, error) {
	backup, err := s.queries.GetBackup(ctx, backupID)
	if err != nil {
		return nil, nil, fmt.Errorf("backup not found: %w", err)
	}
	if backup.Status != "completed" {
		return nil, nil, fmt.Errorf("backup is not completed (status: %s)", backup.Status)
	}
	reader, err := s.store.Download(ctx, backup.S3Key)
	if err != nil {
		return nil, nil, fmt.Errorf("downloading from S3: %w", err)
	}
	return reader, backup, nil
}

// RestoreFromBackup restores the database from a backup stored in S3.
func (s *Service) RestoreFromBackup(ctx context.Context, backupID string) error {
	reader, backup, err := s.DownloadBackup(ctx, backupID)
	if err != nil {
		return err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("reading backup: %w", err)
	}

	return s.restoreFromData(ctx, data, backup.Encrypted, backup.Filename, "")
}

// RestoreFromUpload restores from an uploaded backup file.
func (s *Service) RestoreFromUpload(ctx context.Context, data []byte, encrypted bool, filename, decryptKey string) error {
	return s.restoreFromData(ctx, data, encrypted, filename, decryptKey)
}

func (s *Service) restoreFromData(ctx context.Context, data []byte, encrypted bool, filename, decryptKey string) error {
	// Decrypt if needed
	if encrypted {
		var key []byte
		if decryptKey != "" {
			key, _ = hex.DecodeString(decryptKey)
		} else if len(s.generalBackupKey) > 0 {
			// Derive per-backup key from our general backup key
			pbk, err := crypto.DerivePerBackupKey(s.generalBackupKey, filename)
			if err != nil {
				return fmt.Errorf("deriving per-backup key: %w", err)
			}
			key = pbk
		}
		if len(key) == 0 {
			return fmt.Errorf("encrypted backup but no decryption key available")
		}
		decrypted, err := crypto.Decrypt(key, data)
		if err != nil {
			return fmt.Errorf("decrypting backup: %w", err)
		}
		data = decrypted
	}

	// Extract tarball and restore DB
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("opening gzip: %w", err)
	}
	defer gr.Close()

	// Two-pass restore: first extract everything from the tarball, then
	// restore the DB, then re-encrypt and upload S3 objects.
	var sqlData []byte
	var s3Objects []s3Entry

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		entryData, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("reading tar entry %s: %w", hdr.Name, err)
		}
		if hdr.Name == "database.sql" {
			sqlData = entryData
		} else if strings.HasPrefix(hdr.Name, "s3/") {
			s3Objects = append(s3Objects, s3Entry{
				key:  strings.TrimPrefix(hdr.Name, "s3/"),
				data: entryData,
			})
		}
	}

	// 1. Restore the database first (DEK columns are NULLed by backup).
	if sqlData != nil {
		if err := s.restoreDatabase(sqlData); err != nil {
			return fmt.Errorf("restoring database: %w", err)
		}
	}

	// 2. Re-encrypt S3 objects with fresh DEKs and upload them.
	//    One DEK per analysis, wrapped with this instance's KEK.
	if len(s3Objects) > 0 {
		if err := s.reencryptAndUpload(ctx, s3Objects); err != nil {
			return fmt.Errorf("re-encrypting S3 objects: %w", err)
		}
	}

	// 3. Re-encrypt sensitive config values with this instance's configKEK.
	if s.enc != nil {
		if err := s.reencryptConfigValues(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to re-encrypt config values")
		}
	}

	return nil
}

func (s *Service) restoreDatabase(sqlData []byte) error {
	cmd := exec.Command("psql", s.cfg.DatabaseURL)
	cmd.Stdin = bytes.NewReader(sqlData)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql: %s: %w", stderr.String(), err)
	}
	return nil
}

// reencryptAndUpload generates a fresh DEK per analysis, encrypts the
// plaintext S3 objects, uploads them, and stores the wrapped DEK in the DB.
// Objects not under analyses/ are uploaded as-is.
func (s *Service) reencryptAndUpload(ctx context.Context, objects []s3Entry) error {
	type analysisDEK struct {
		dek          []byte
		encryptedDEK []byte
		nonce        []byte
	}
	dekMap := make(map[string]*analysisDEK) // analysisID → DEK info

	for _, obj := range objects {
		data := obj.data

		// If encryption is available and this is an analysis object, re-encrypt.
		if s.enc != nil && strings.HasPrefix(obj.key, "analyses/") {
			parts := strings.SplitN(obj.key, "/", 3)
			if len(parts) >= 3 {
				analysisID := parts[1]

				ad, ok := dekMap[analysisID]
				if !ok {
					// Generate a fresh DEK for this analysis.
					dek, err := crypto.GenerateDEK()
					if err != nil {
						return fmt.Errorf("generating DEK for analysis %s: %w", analysisID, err)
					}
					encDEK, nonce, err := s.enc.WrapDEK(dek)
					if err != nil {
						return fmt.Errorf("wrapping DEK for analysis %s: %w", analysisID, err)
					}
					ad = &analysisDEK{dek: dek, encryptedDEK: encDEK, nonce: nonce}
					dekMap[analysisID] = ad
				}

				ciphertext, err := crypto.Encrypt(ad.dek, data)
				if err != nil {
					return fmt.Errorf("encrypting %s: %w", obj.key, err)
				}
				data = ciphertext
			}
		}

		if err := s.store.Upload(ctx, obj.key, bytes.NewReader(data), int64(len(data)), "application/octet-stream"); err != nil {
			log.Warn().Err(err).Str("key", obj.key).Msg("Failed to restore S3 object")
		}
	}

	// Update the analyses table with the new wrapped DEKs.
	for analysisID, ad := range dekMap {
		if _, err := s.queries.Pool().Exec(ctx,
			`UPDATE analyses SET encrypted_dek = $2, dek_nonce = $3, updated_at = NOW() WHERE id = $1`,
			analysisID, ad.encryptedDEK, ad.nonce); err != nil {
			log.Error().Err(err).Str("analysis_id", analysisID).Msg("Failed to store new DEK for analysis")
		}
	}

	return nil
}

// DeleteBackupByID deletes a backup record and its S3 object.
func (s *Service) DeleteBackupByID(ctx context.Context, backupID string) error {
	backup, err := s.queries.GetBackup(ctx, backupID)
	if err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}
	if backup.S3Key != "" {
		if err := s.store.Delete(ctx, backup.S3Key); err != nil {
			log.Error().Err(err).Str("s3_key", backup.S3Key).Msg("Failed to delete backup from S3")
		}
	}
	return s.queries.DeleteBackup(ctx, backupID)
}

// DeleteFailedBackups removes all backups with status "failed".
func (s *Service) DeleteFailedBackups(ctx context.Context) (int64, error) {
	return s.queries.DeleteFailedBackups(ctx)
}
