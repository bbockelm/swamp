// Package crypto provides envelope encryption for documents and backups.
//
// Architecture:
//   - A project master key (32 bytes, hex-encoded) is provided at startup via config.
//   - Using HKDF-SHA256 we derive a key-encryption key (KEK) scoped to this database.
//   - Each document gets a random 256-bit data encryption key (DEK).
//   - The document plaintext is encrypted with AES-256-GCM using the DEK.
//   - The DEK itself is encrypted (wrapped) with AES-256-GCM using the KEK and
//     stored in the database alongside its nonce.
//   - Even if the database AND S3 are both compromised, the master key is needed.
//
// Backup key hierarchy:
//   - Master key -> HKDF("swamp-backup-key") -> General Backup Key (shareable)
//   - General Backup Key + filename -> HKDF(filename as salt) -> Per-Backup Key
//   - Per-Backup Key wraps the random DEK for that backup's archive.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Encryptor performs envelope encryption for documents.
type Encryptor struct {
	kek       []byte // key-encryption key for documents
	configKEK []byte // key-encryption key for app_config secrets
}

// NewEncryptor creates an Encryptor from a hex-encoded master key.
func NewEncryptor(masterKeyHex string) (*Encryptor, error) {
	if masterKeyHex == "" {
		return nil, errors.New("INSTANCE_KEY is required")
	}
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decoding master key hex: %w", err)
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}

	hkdfReader := hkdf.New(sha256.New, masterKey, nil, []byte("swamp-docs-kek"))
	kek := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, kek); err != nil {
		return nil, fmt.Errorf("deriving KEK: %w", err)
	}

	configReader := hkdf.New(sha256.New, masterKey, nil, []byte("swamp-config-kek"))
	configKEK := make([]byte, 32)
	if _, err := io.ReadFull(configReader, configKEK); err != nil {
		return nil, fmt.Errorf("deriving config KEK: %w", err)
	}

	return &Encryptor{kek: kek, configKEK: configKEK}, nil
}

// GenerateDEK creates a random 256-bit data encryption key.
func GenerateDEK() ([]byte, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("generating DEK: %w", err)
	}
	return dek, nil
}

// WrapDEK encrypts a DEK with the KEK using AES-256-GCM.
func (e *Encryptor) WrapDEK(dek []byte) (encryptedDEK, nonce []byte, err error) {
	block, err := aes.NewCipher(e.kek)
	if err != nil {
		return nil, nil, fmt.Errorf("creating KEK cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("creating KEK GCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generating nonce: %w", err)
	}
	encryptedDEK = gcm.Seal(nil, nonce, dek, nil)
	return encryptedDEK, nonce, nil
}

// UnwrapDEK decrypts a DEK that was encrypted with WrapDEK.
func (e *Encryptor) UnwrapDEK(encryptedDEK, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.kek)
	if err != nil {
		return nil, fmt.Errorf("creating KEK cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating KEK GCM: %w", err)
	}
	dek, err := gcm.Open(nil, nonce, encryptedDEK, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting DEK (wrong master key?): %w", err)
	}
	return dek, nil
}

// Encrypt encrypts plaintext with the given DEK using AES-256-GCM.
// The nonce is prepended to the ciphertext.
func Encrypt(dek, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("creating data cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating data GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating data nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext that was encrypted with Encrypt.
func Decrypt(dek, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("creating data cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating data GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, enc := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting data: %w", err)
	}
	return plaintext, nil
}

// --- Config value encryption ---

const configEncPrefix = "enc:v1:"

// EncryptConfigValue encrypts a plaintext string for storage in app_config.
func (e *Encryptor) EncryptConfigValue(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.configKEK)
	if err != nil {
		return "", fmt.Errorf("creating config cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating config GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating config nonce: %w", err)
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return configEncPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptConfigValue decrypts a value produced by EncryptConfigValue.
func (e *Encryptor) DecryptConfigValue(stored string) (string, error) {
	if !IsEncryptedConfig(stored) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(configEncPrefix):])
	if err != nil {
		return "", fmt.Errorf("decoding config ciphertext: %w", err)
	}
	block, err := aes.NewCipher(e.configKEK)
	if err != nil {
		return "", fmt.Errorf("creating config cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating config GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted config value too short")
	}
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting config value (wrong master key?): %w", err)
	}
	return string(pt), nil
}

// IsEncryptedConfig returns true if the value string has the encryption prefix.
func IsEncryptedConfig(val string) bool {
	return len(val) > len(configEncPrefix) && val[:len(configEncPrefix)] == configEncPrefix
}

// --- Backup key hierarchy ---

// DeriveBackupKey derives the general backup key from a master key hex string.
func DeriveBackupKey(masterKeyHex string) ([]byte, error) {
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decoding master key hex: %w", err)
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte("swamp-backup-key"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving backup key: %w", err)
	}
	return key, nil
}

// DerivePerBackupKey derives a per-backup key from the general backup key.
func DerivePerBackupKey(generalBackupKey []byte, backupFilename string) ([]byte, error) {
	if len(generalBackupKey) != 32 {
		return nil, fmt.Errorf("general backup key must be 32 bytes, got %d", len(generalBackupKey))
	}
	r := hkdf.New(sha256.New, generalBackupKey, []byte(backupFilename), []byte("swamp-per-backup-key"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving per-backup key: %w", err)
	}
	return key, nil
}

// WrapDEKWithKey wraps a DEK using an arbitrary 32-byte key.
func WrapDEKWithKey(key, dek []byte) (encryptedDEK, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("creating GCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generating nonce: %w", err)
	}
	encryptedDEK = gcm.Seal(nil, nonce, dek, nil)
	return encryptedDEK, nonce, nil
}

// UnwrapDEKWithKey unwraps a DEK using an arbitrary 32-byte key.
func UnwrapDEKWithKey(key, encryptedDEK, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	dek, err := gcm.Open(nil, nonce, encryptedDEK, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting DEK: %w", err)
	}
	return dek, nil
}
