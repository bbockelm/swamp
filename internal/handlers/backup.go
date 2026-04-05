package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// --- Backups ---

func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := h.queries.ListBackups(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list backups")
		respondError(w, http.StatusInternalServerError, "Failed to list backups")
		return
	}
	if backups == nil {
		backups = []models.Backup{}
	}
	respondJSON(w, http.StatusOK, backups)
}

func (h *Handler) TriggerBackup(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	backup, err := h.backupSvc.StartBackup(r.Context(), user.ID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to start backup")
		respondError(w, http.StatusInternalServerError, "Failed to start backup")
		return
	}
	go h.backupSvc.RunBackup(context.Background(), backup)
	respondJSON(w, http.StatusAccepted, backup)
}

func (h *Handler) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	backupID := chi.URLParam(r, "backupID")
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	reader, backup, err := h.backupSvc.DownloadBackup(r.Context(), backupID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	defer func() { _ = reader.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, backup.Filename))
	if _, err := io.Copy(w, reader); err != nil {
		log.Error().Err(err).Str("backup_id", backupID).Msg("Error streaming backup download")
	}
}

func (h *Handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	backupID := chi.URLParam(r, "backupID")
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	if err := h.backupSvc.RestoreFromBackup(r.Context(), backupID); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "restore completed"})
}

func (h *Handler) UploadRestore(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30) // 2GB limit
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "Missing file in upload")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to read uploaded file")
		return
	}
	encrypted := r.FormValue("encrypted") != "false"
	decryptKey := r.FormValue("decrypt_key")
	filename := fileHeader.Filename
	if err := h.backupSvc.RestoreFromUpload(r.Context(), data, encrypted, filename, decryptKey); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "restore completed"})
}

func (h *Handler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	backupID := chi.URLParam(r, "backupID")
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	if err := h.backupSvc.DeleteBackupByID(r.Context(), backupID); err != nil {
		log.Error().Err(err).Msg("Failed to delete backup")
		respondError(w, http.StatusInternalServerError, "Failed to delete backup")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) DeleteFailedBackups(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	count, err := h.backupSvc.DeleteFailedBackups(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to delete failed backups")
		respondError(w, http.StatusInternalServerError, "Failed to delete failed backups")
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"status": "deleted", "count": count})
}

func (h *Handler) GetPerBackupKey(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	backupID := chi.URLParam(r, "backupID")
	backup, err := h.queries.GetBackup(r.Context(), backupID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Backup not found")
		return
	}
	key, err := h.backupSvc.PerBackupKeyHex(backup.Filename)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get per-backup key")
		respondError(w, http.StatusInternalServerError, "Failed to get encryption key")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"key": key, "filename": backup.Filename})
}

func (h *Handler) GetGeneralBackupKey(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	key, err := h.backupSvc.GeneralBackupKeyHex()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get general backup key")
		respondError(w, http.StatusInternalServerError, "Failed to get encryption key")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (h *Handler) GetBackupSettings(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	settings := h.backupSvc.GetSettings(r.Context())
	respondJSON(w, http.StatusOK, settings)
}

func (h *Handler) UpdateBackupSettings(w http.ResponseWriter, r *http.Request) {
	if h.backupSvc == nil {
		respondError(w, http.StatusServiceUnavailable, "Backup service not configured")
		return
	}
	var settings models.BackupSettings
	if err := decodeJSON(r, &settings); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := h.backupSvc.SaveSettings(r.Context(), settings); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, settings)
}
