package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
	"github.com/bbockelm/swamp/internal/storage"
)

// gatherAnalysisContext collects prior findings, annotations, and notes from
// recent completed analyses to inject into the prompt as context. This is a
// shared helper used by both the local Executor and the ProcessExecutor.
func gatherAnalysisContext(ctx context.Context, queries *db.Queries, enc *crypto.Encryptor, store *storage.Store, projectID string, packages []models.SoftwarePackage) *models.AnalysisContext {
	ac := &models.AnalysisContext{}

	// Get open findings for this project.
	findings, err := queries.GetOpenFindingsSummary(ctx, projectID)
	if err != nil {
		log.Warn().Err(err).Str("project_id", projectID).Msg("Failed to get open findings for context")
	} else {
		ac.OpenFindings = findings
	}

	// Determine the branch being analyzed (use first package).
	branch := ""
	if len(packages) > 0 {
		branch = packages[0].GitBranch
	}

	// Get notes from recent completed analyses.
	noteRefs, err := queries.GetRecentAnalysisNotes(ctx, projectID, branch, 3)
	if err != nil {
		log.Warn().Err(err).Str("project_id", projectID).Msg("Failed to get recent analysis notes")
	} else {
		for _, ref := range noteRefs {
			content, err := decryptNoteFromS3(ctx, enc, store, ref)
			if err != nil {
				log.Warn().Err(err).Str("s3_key", ref.S3Key).Msg("Failed to decrypt analysis note")
				continue
			}
			ac.PriorNotes = append(ac.PriorNotes, content)
		}
	}

	if len(ac.OpenFindings) > 0 || len(ac.PriorNotes) > 0 {
		log.Info().
			Int("open_findings", len(ac.OpenFindings)).
			Int("prior_notes", len(ac.PriorNotes)).
			Str("project_id", projectID).
			Msg("Gathered analysis context from prior runs")
	}

	return ac
}

// decryptNoteFromS3 downloads and decrypts an analysis notes file from S3.
func decryptNoteFromS3(ctx context.Context, enc *crypto.Encryptor, store *storage.Store, ref models.AnalysisNoteRef) (string, error) {
	if len(ref.EncryptedDEK) == 0 {
		return "", fmt.Errorf("analysis %s has no DEK", ref.AnalysisID)
	}
	dek, err := enc.UnwrapDEK(ref.EncryptedDEK, ref.DEKNonce)
	if err != nil {
		return "", fmt.Errorf("unwrap DEK: %w", err)
	}
	rc, err := store.Download(ctx, ref.S3Key)
	if err != nil {
		return "", fmt.Errorf("download from S3: %w", err)
	}
	defer func() { _ = rc.Close() }()
	ciphertext, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read from S3: %w", err)
	}
	plaintext, err := crypto.Decrypt(dek, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
