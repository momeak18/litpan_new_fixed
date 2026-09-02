package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"litepan/internal/domain"
	"litepan/internal/javsp"
)

func (h *Handler) getJavSPConfig(w http.ResponseWriter, _ *http.Request) {
	if h.javsp == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "JavSP service is not available"))
		return
	}
	writeOK(w, h.javsp.Config())
}
func (h *Handler) putJavSPConfig(w http.ResponseWriter, r *http.Request) {
	if h.javsp == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "JavSP service is not available"))
		return
	}
	var cfg javsp.Config
	if err := decodeJSON(r, &cfg); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.javsp.SetConfig(cfg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
func (h *Handler) listJavSPTasks(w http.ResponseWriter, _ *http.Request) {
	if h.javsp == nil {
		writeOK(w, []javsp.Task{})
		return
	}
	writeOK(w, h.javsp.List())
}
func (h *Handler) createJavSPTask(w http.ResponseWriter, r *http.Request) {
	if h.javsp == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "JavSP service is not available"))
		return
	}
	var in struct {
		RelativePath string `json:"relative_path"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	task, err := h.javsp.Create(in.RelativePath)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, task)
}
func (h *Handler) cancelJavSPTask(w http.ResponseWriter, r *http.Request) {
	if h.javsp == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "JavSP service is not available"))
		return
	}
	if err := h.javsp.Cancel(strings.TrimSpace(chi.URLParam(r, "id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

