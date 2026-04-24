package handlers

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/bbockelm/swamp/internal/docs"
)

// GetPublicOnboardingTutorial returns the canonical onboarding tutorial markdown.
func (h *Handler) GetPublicOnboardingTutorial(w http.ResponseWriter, r *http.Request) {
	fsys, err := docs.ContentFS()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Tutorial content is unavailable")
		return
	}
	data, err := fs.ReadFile(fsys, "tutorials/onboarding.md")
	if err != nil {
		respondError(w, http.StatusNotFound, "Tutorial not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"title":           "SWAMP Onboarding Tutorial",
		"markdown":        string(data),
		"image_base_path": "/api/v1/tutorials/images/",
	})
}

// GetPublicTutorialImage serves onboarding tutorial images from embedded docs content.
func (h *Handler) GetPublicTutorialImage(w http.ResponseWriter, r *http.Request) {
	fsys, err := docs.ContentFS()
	if err != nil {
		http.Error(w, "tutorial content is unavailable", http.StatusInternalServerError)
		return
	}

	fileName := path.Base(strings.TrimSpace(r.PathValue("file")))
	if fileName == "" || fileName == "." || fileName == ".." {
		http.Error(w, "invalid image name", http.StatusBadRequest)
		return
	}

	data, err := fs.ReadFile(fsys, path.Join("tutorials/images", fileName))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if contentType := mime.TypeByExtension(path.Ext(fileName)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}
