package oauth2

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	fositejwt "github.com/ory/fosite/token/jwt"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/hkdf"

	"github.com/bbockelm/swamp/internal/crypto"
)

// Provider wraps the fosite OAuth2Provider and associated resources.
type Provider struct {
	fosite.OAuth2Provider
	Storage    *Storage
	PrivateKey *rsa.PrivateKey
	KID        string
}

// NewProvider creates a fosite-backed OAuth2 provider with Postgres storage.
// The signing key is loaded from the DB or generated on first run.
// hmacSecret should be derived from the instance's master key.
func NewProvider(ctx context.Context, pool *pgxpool.Pool, issuerURL string, masterKeyHex string) (*Provider, error) {
	store := NewStorage(pool)

	// Derive HMAC secret from master key for opaque token signing.
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	hmacSecret := make([]byte, 32)
	r := hkdf.New(sha256.New, masterKey, nil, []byte("swamp-oauth2-hmac"))
	if _, err := io.ReadFull(r, hmacSecret); err != nil {
		return nil, fmt.Errorf("derive HMAC secret: %w", err)
	}

	// Create encryptor for envelope-encrypting the signing key.
	enc, err := crypto.NewEncryptor(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("create encryptor: %w", err)
	}

	// Load or generate RSA signing key.
	privKey, kid, err := getOrCreateSigningKey(ctx, pool, enc)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}

	fositeConfig := &fosite.Config{
		GlobalSecret:           hmacSecret,
		AccessTokenLifespan:    1 * time.Hour,
		RefreshTokenLifespan:   7 * 24 * time.Hour,
		AuthorizeCodeLifespan:  10 * time.Minute,
		IDTokenLifespan:        1 * time.Hour,
		AccessTokenIssuer:      issuerURL,
		IDTokenIssuer:          issuerURL,
		ScopeStrategy:          fosite.WildcardScopeStrategy,
		AudienceMatchingStrategy: fosite.DefaultAudienceMatchingStrategy,
		EnforcePKCE:            true,
		EnforcePKCEForPublicClients: true,
	}

	keyGetter := func(_ context.Context) (any, error) {
		return privKey, nil
	}

	hmacStrategy := compose.NewOAuth2HMACStrategy(fositeConfig)
	oidcStrategy := compose.NewOpenIDConnectStrategy(keyGetter, fositeConfig)

	strategy := &compose.CommonStrategy{
		CoreStrategy:               hmacStrategy,
		OpenIDConnectTokenStrategy: oidcStrategy,
		Signer:                     &fositejwt.DefaultSigner{GetPrivateKey: keyGetter},
	}

	provider := compose.Compose(
		fositeConfig,
		store,
		strategy,
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2PKCEFactory,
		compose.OAuth2TokenRevocationFactory,
		compose.OAuth2TokenIntrospectionFactory,
		compose.OpenIDConnectExplicitFactory,
	)

	return &Provider{
		OAuth2Provider: provider,
		Storage:        store,
		PrivateKey:     privKey,
		KID:            kid,
	}, nil
}

// getOrCreateSigningKey loads the RSA signing key from the database,
// or generates a new 2048-bit key if none exists.
// The private key is encrypted at rest using envelope encryption.
func getOrCreateSigningKey(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) (*rsa.PrivateKey, string, error) {
	var kid string
	var privPEM string          // plaintext PEM (legacy rows)
	var encPrivKey, encDEK, dekNonce []byte // encrypted columns (new rows)

	err := pool.QueryRow(ctx, `
		SELECT kid, private_key_pem, encrypted_private_key, encrypted_dek, dek_nonce
		FROM oauth2_signing_keys WHERE id = 'default'`,
	).Scan(&kid, &privPEM, &encPrivKey, &encDEK, &dekNonce)

	if err == nil {
		var key *rsa.PrivateKey

		if len(encDEK) > 0 && len(encPrivKey) > 0 {
			// Decrypt using envelope encryption.
			dek, err := enc.UnwrapDEK(encDEK, dekNonce)
			if err != nil {
				return nil, "", fmt.Errorf("unwrap signing key DEK: %w", err)
			}
			plaintext, err := crypto.Decrypt(dek, encPrivKey)
			if err != nil {
				return nil, "", fmt.Errorf("decrypt signing key: %w", err)
			}
			block, _ := pem.Decode(plaintext)
			if block == nil {
				return nil, "", fmt.Errorf("invalid PEM in decrypted signing key")
			}
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, "", fmt.Errorf("parse decrypted private key: %w", err)
			}
			log.Info().Str("kid", kid).Msg("Loaded encrypted OAuth2 signing key from database")
		} else if privPEM != "" {
			// Legacy: plaintext PEM. Decrypt and auto-encrypt.
			block, _ := pem.Decode([]byte(privPEM))
			if block == nil {
				return nil, "", fmt.Errorf("invalid PEM in signing key")
			}
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, "", fmt.Errorf("parse private key: %w", err)
			}
			log.Warn().Str("kid", kid).Msg("Found unencrypted OAuth2 signing key, encrypting at rest")
			if err := encryptSigningKeyInPlace(ctx, pool, enc, []byte(privPEM)); err != nil {
				log.Error().Err(err).Msg("Failed to encrypt existing signing key (will retry on next restart)")
			}
		} else {
			return nil, "", fmt.Errorf("signing key row exists but has no key data")
		}
		return key, kid, nil
	}
	if err != pgx.ErrNoRows {
		return nil, "", fmt.Errorf("query signing key: %w", err)
	}

	// Generate new key.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", fmt.Errorf("generate RSA key: %w", err)
	}

	privBytes := x509.MarshalPKCS1PrivateKey(key)
	privPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})

	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", fmt.Errorf("marshal public key: %w", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))

	// Generate a stable key ID from the public key hash.
	kidHash := sha256.Sum256(pubBytes)
	kid = hex.EncodeToString(kidHash[:8])

	// Encrypt the private key using envelope encryption.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return nil, "", fmt.Errorf("generate DEK for signing key: %w", err)
	}
	encryptedPEM, err := crypto.Encrypt(dek, privPEMBytes)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt signing key: %w", err)
	}
	wrappedDEK, wrappedNonce, err := enc.WrapDEK(dek)
	if err != nil {
		return nil, "", fmt.Errorf("wrap signing key DEK: %w", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO oauth2_signing_keys (id, kid, algorithm, private_key_pem, public_key_pem,
		                                 encrypted_private_key, encrypted_dek, dek_nonce)
		VALUES ('default', $1, 'RS256', '', $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING`,
		kid, pubPEM, encryptedPEM, wrappedDEK, wrappedNonce)
	if err != nil {
		return nil, "", fmt.Errorf("store signing key: %w", err)
	}

	log.Info().Str("kid", kid).Msg("Generated and stored new encrypted OAuth2 signing key")
	return key, kid, nil
}

// encryptSigningKeyInPlace encrypts a legacy plaintext PEM key and updates the row.
func encryptSigningKeyInPlace(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, privPEM []byte) error {
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return fmt.Errorf("generate DEK: %w", err)
	}
	encryptedPEM, err := crypto.Encrypt(dek, privPEM)
	if err != nil {
		return fmt.Errorf("encrypt PEM: %w", err)
	}
	wrappedDEK, wrappedNonce, err := enc.WrapDEK(dek)
	if err != nil {
		return fmt.Errorf("wrap DEK: %w", err)
	}
	_, err = pool.Exec(ctx, `
		UPDATE oauth2_signing_keys
		SET private_key_pem = '', encrypted_private_key = $1, encrypted_dek = $2, dek_nonce = $3
		WHERE id = 'default'`,
		encryptedPEM, wrappedDEK, wrappedNonce)
	return err
}
