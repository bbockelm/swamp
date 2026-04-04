package handlers

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
)

// --- Analysis Results ---

func (h *Handler) ListResults(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")
	results, err := h.queries.ListAnalysisResults(r.Context(), analysisID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list results")
		respondError(w, http.StatusInternalServerError, "Failed to list results")
		return
	}
	respondJSON(w, http.StatusOK, results)
}

func (h *Handler) GetResult(w http.ResponseWriter, r *http.Request) {
	resultID := chi.URLParam(r, "resultID")
	result, err := h.queries.GetAnalysisResult(r.Context(), resultID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Result not found")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// DownloadResultArtifact streams the result file from S3, decrypting it
// with the per-analysis DEK.
func (h *Handler) DownloadResultArtifact(w http.ResponseWriter, r *http.Request) {
	resultID := chi.URLParam(r, "resultID")
	result, err := h.queries.GetAnalysisResult(r.Context(), resultID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Result not found")
		return
	}

	// Load the analysis to get the encrypted DEK.
	analysis, err := h.queries.GetAnalysis(r.Context(), result.AnalysisID)
	if err != nil {
		log.Error().Err(err).Str("analysis_id", result.AnalysisID).Msg("Failed to load analysis for decryption")
		respondError(w, http.StatusInternalServerError, "Failed to download artifact")
		return
	}

	reader, err := h.store.Download(r.Context(), result.S3Key)
	if err != nil {
		log.Error().Err(err).Str("s3_key", result.S3Key).Msg("Failed to download artifact")
		respondError(w, http.StatusInternalServerError, "Failed to download artifact")
		return
	}
	defer reader.Close()

	ciphertext, err := io.ReadAll(reader)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read artifact from S3")
		respondError(w, http.StatusInternalServerError, "Failed to download artifact")
		return
	}

	// Decrypt if the analysis has a DEK (new analyses); serve raw for legacy data.
	var plaintext []byte
	if len(analysis.EncryptedDEK) > 0 {
		dek, err := h.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
		if err != nil {
			log.Error().Err(err).Msg("Failed to unwrap analysis DEK")
			respondError(w, http.StatusInternalServerError, "Failed to decrypt artifact")
			return
		}
		plaintext, err = crypto.Decrypt(dek, ciphertext)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decrypt artifact")
			respondError(w, http.StatusInternalServerError, "Failed to decrypt artifact")
			return
		}
	} else {
		plaintext = ciphertext
	}

	w.Header().Set("Content-Type", result.ContentType)
	if result.Filename != "" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+result.Filename+"\"")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(plaintext)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(plaintext); err != nil {
		log.Error().Err(err).Msg("Failed to write artifact response")
	}
}
