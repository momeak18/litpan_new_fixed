package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/localextract"
)

type createLocalExtractTasksReq struct {
	AccountID         int64    `json:"account_id"`
	SourceFileIDs     []string `json:"source_file_ids"`
	SourceFileNames   []string `json:"source_file_names"`
	TargetParentID    string   `json:"target_parent_id"`
	TargetDisplayPath string   `json:"target_display_path"`
}

func (h *Handler) createLocalExtractTasks(w http.ResponseWriter, r *http.Request) {
	if h.localExtracts == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "本地解压队列未启用"))
		return
	}
	var req createLocalExtractTasksReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 || len(req.SourceFileIDs) == 0 || len(req.SourceFileIDs) != len(req.SourceFileNames) {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请选择至少一个压缩包"))
		return
	}
	tasks := make([]*localextract.Task, 0, len(req.SourceFileIDs))
	for i, fileID := range req.SourceFileIDs {
		task, err := h.localExtracts.Create(r.Context(), localextract.CreateParams{
			AccountID: req.AccountID, SourceFileID: fileID, SourceFileName: req.SourceFileNames[i],
			TargetParentID: req.TargetParentID, TargetDisplayPath: req.TargetDisplayPath,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		tasks = append(tasks, task)
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "本地解压队列已创建", Data: tasks})
}

func (h *Handler) listLocalExtractTasks(w http.ResponseWriter, r *http.Request) {
	if h.localExtracts == nil {
		writeJSON(w, http.StatusOK, Resp{Success: true, Data: []localextract.Task{}})
		return
	}
	var accountID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
			return
		}
		accountID = id
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取本地解压任务成功", Data: h.localExtracts.List(r.Context(), accountID)})
}

func (h *Handler) deleteLocalExtractTask(w http.ResponseWriter, r *http.Request) {
	if h.localExtracts == nil {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "本地解压队列未启用"))
		return
	}
	if err := h.localExtracts.Delete(r.Context(), strings.TrimSpace(chi.URLParam(r, "taskID"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "本地解压任务已删除"})
}
