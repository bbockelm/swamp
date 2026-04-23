package handlers

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
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
	defer func() { _ = reader.Close() }()

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

// RetrySARIFUpload re-attempts GitHub SARIF upload for all SARIF results
// in an analysis. This is useful when the initial upload was skipped (e.g.
// because the package didn't have SARIF upload enabled at analysis time)
// or when a transient error occurred.
func (h *Handler) RetrySARIFUpload(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")

	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	results, err := h.queries.ListAnalysisResults(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list results")
		return
	}

	// Get the analysis DEK for decrypting SARIF files.
	var dek []byte
	if len(analysis.EncryptedDEK) > 0 && h.encryptor != nil {
		dek, err = h.encryptor.UnwrapDEK(analysis.EncryptedDEK, analysis.DEKNonce)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to unwrap encryption key")
			return
		}
	}

	// Load linked packages for this analysis.
	packages, err := h.queries.ListAnalysisPackages(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to load packages")
		return
	}
	pkgByID := make(map[string]*models.SoftwarePackage, len(packages))
	for i := range packages {
		pkgByID[packages[i].ID] = &packages[i]
	}

	type resultStatus struct {
		ResultID string `json:"result_id"`
		Filename string `json:"filename"`
		Uploaded bool   `json:"uploaded"`
		URL      string `json:"url,omitempty"`
		Error    string `json:"error,omitempty"`
	}

	var uploaded, attempted int
	var statuses []resultStatus

	for _, result := range results {
		if result.ResultType != "sarif" {
			continue
		}

		// Download and decrypt the SARIF file.
		reader, err := h.store.Download(r.Context(), result.S3Key)
		if err != nil {
			statuses = append(statuses, resultStatus{
				ResultID: result.ID, Filename: result.Filename,
				Error: "Failed to download from storage",
			})
			attempted++
			continue
		}
		ciphertext, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			statuses = append(statuses, resultStatus{
				ResultID: result.ID, Filename: result.Filename,
				Error: "Failed to read from storage",
			})
			attempted++
			continue
		}

		var plaintext []byte
		if dek != nil {
			plaintext, err = crypto.Decrypt(dek, ciphertext)
			if err != nil {
				statuses = append(statuses, resultStatus{
					ResultID: result.ID, Filename: result.Filename,
					Error: "Failed to decrypt",
				})
				attempted++
				continue
			}
		} else {
			plaintext = ciphertext
		}

		attempted++

		// Find the package for this result.
		var pkg *models.SoftwarePackage
		if result.PackageID != nil && *result.PackageID != "" {
			pkg = pkgByID[*result.PackageID]
		}
		// Fall back to the only package if there's just one.
		if pkg == nil && len(packages) == 1 {
			pkg = &packages[0]
		}

		var uploadURL, uploadErr string
		if pkg != nil && pkg.SARIFUploadEnabled && pkg.GitHubOwner != "" && pkg.GitHubRepo != "" {
			url, err := h.ghClient.UploadSARIFForPackage(r.Context(), pkg, plaintext, analysis.GitCommit)
			if err != nil {
				uploadErr = err.Error()
			} else {
				uploadURL = url
			}
		} else {
			// Try project-level config.
			if ghCfg, cfgErr := h.queries.GetProjectGitHubConfig(r.Context(), analysis.ProjectID); cfgErr == nil && ghCfg.SARIFUploadEnabled && ghCfg.InstallationID != 0 {
				url, err := h.ghClient.UploadSARIFForProject(r.Context(), analysis.ProjectID, plaintext, analysis.GitCommit)
				if err != nil {
					uploadErr = err.Error()
				} else {
					uploadURL = url
				}
			} else {
				uploadErr = "No package or project has SARIF upload enabled for this result"
			}
		}

		// Persist the upload status.
		if uploadURL == "" && uploadErr == "" {
			uploadErr = "Upload attempted but no GitHub alerts URL was returned"
		}
		_ = h.queries.SetResultSARIFUploadStatus(r.Context(), result.ID, true, uploadURL, uploadErr)

		if uploadURL != "" {
			uploaded++
			_ = h.queries.SetAnalysisSARIFUploadURL(r.Context(), analysisID, uploadURL)
		}

		statuses = append(statuses, resultStatus{
			ResultID: result.ID,
			Filename: result.Filename,
			Uploaded: uploadURL != "",
			URL:      uploadURL,
			Error:    uploadErr,
		})
	}

	log.Info().
		Str("analysis_id", analysisID).
		Int("attempted", attempted).
		Int("uploaded", uploaded).
		Msg("SARIF upload retry completed")

	respondJSON(w, http.StatusOK, map[string]any{
		"attempted": attempted,
		"uploaded":  uploaded,
		"results":   statuses,
	})
}
