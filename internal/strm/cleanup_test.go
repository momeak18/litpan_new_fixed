package strm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupScopedStaleFilesRemovesSameStemSidecarsOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepStrm := filepath.Join(dir, "保留.strm")
	staleStrm := filepath.Join(dir, "过期.strm")
	staleNFO := filepath.Join(dir, "过期.nfo")
	stalePoster := filepath.Join(dir, "过期-poster.jpg")
	staleThumb := filepath.Join(dir, "过期-thumb.jpg")
	folderNFO := filepath.Join(dir, "tvshow.nfo")
	folderPoster := filepath.Join(dir, "poster.jpg")
	keepNFO := filepath.Join(dir, "保留.nfo")
	for _, p := range []string{keepStrm, staleStrm, staleNFO, stalePoster, staleThumb, folderNFO, folderPoster, keepNFO} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]struct{}{
		"任务/电影/保留.strm": {},
	}
	removed, err := cleanupScopedStaleFiles(
		root,
		"任务",
		seen,
		[]cleanupScope{{relDirs: nil, recursive: true}},
		nil,
		NewFailureCollector(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	for _, p := range []string{staleStrm, staleNFO, stalePoster, staleThumb} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", filepath.Base(p))
		}
	}
	for _, p := range []string{keepStrm, keepNFO, folderNFO, folderPoster} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 应保留: %v", filepath.Base(p), err)
		}
	}
}
