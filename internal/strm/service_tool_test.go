package strm

import (
	"testing"

	"litepan/internal/domain"
)

func TestReconcileDirCacheOnlyUpdatesMatchedMismatched(t *testing.T) {
	path := "云影音/影视库"
	children := []domain.FileItem{
		{ID: "d-new", Name: "新目录", IsDir: true},
		{ID: "d-renamed", Name: "Season 01", IsDir: true},
		{ID: "f-movie", Name: "电影A.mkv"},
	}
	candidates := reconcileCandidates(1, "d-parent", path, children)
	if len(candidates) != 3 {
		t.Fatalf("候选应为 当前目录+2个子目录，实际 %d", len(candidates))
	}

	hit := map[string]string{
		"d-parent":  "云影音/影视库",
		"d-renamed": "云影音/影视库/Season 1",
		// d-new 未命中
	}
	var updates []domain.StrmDirCacheEntry
	for _, c := range candidates {
		old, ok := hit[c.DirID]
		if ok && old != c.DirPath {
			updates = append(updates, c)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("应只更新 1 条（改名目录），实际 %d", len(updates))
	}
	if updates[0].DirID != "d-renamed" || updates[0].DirPath != "云影音/影视库/Season 01" {
		t.Fatalf("更新的应是改名后的目录，实际 %+v", updates[0])
	}
}
