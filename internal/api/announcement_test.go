package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"litepan/internal/settings"
)

type announcementConfigRepo struct {
	values map[string]string
}

func (r *announcementConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := r.values[key]
	return value, ok, nil
}

func (r *announcementConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *announcementConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestMarkAnnouncementReadPersistsVersion(t *testing.T) {
	repo := &announcementConfigRepo{values: map[string]string{}}
	settingsSvc, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{settings: settingsSvc}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/announcement/read", bytes.NewBufferString(`{"notice_version":" 2026-08-24-1 "}`))
	response := httptest.NewRecorder()
	handler.markAnnouncementRead(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := settingsSvc.String(settings.KeyAnnouncementReadVersion); got != "2026-08-24-1" {
		t.Fatalf("已读版本=%q", got)
	}
	if !isAnnouncementVersionRead(settingsSvc, "2026-08-24-1") {
		t.Fatal("相同公告版本应判定为已读")
	}
	reloaded, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !isAnnouncementVersionRead(reloaded, "2026-08-24-1") {
		t.Fatal("重新加载 settings 后应保留已读版本")
	}
	if isAnnouncementVersionRead(settingsSvc, "2026-08-24-2") {
		t.Fatal("新公告版本不应判定为已读")
	}
}

func TestMarkAnnouncementReadRejectsEmptyVersion(t *testing.T) {
	repo := &announcementConfigRepo{values: map[string]string{}}
	settingsSvc, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{settings: settingsSvc}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/announcement/read", bytes.NewBufferString(`{"notice_version":" "}`))
	response := httptest.NewRecorder()
	handler.markAnnouncementRead(response, request)

	if response.Code == http.StatusOK {
		t.Fatalf("空公告版本应拒绝: body=%s", response.Body.String())
	}
}
