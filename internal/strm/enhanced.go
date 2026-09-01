package strm

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func useEnhancedScan(ctx context.Context, task *domain.StrmTask, deps ScanDeps, runMode string) (bool, error) {
	if !deps.Settings.Tool115TreeEnabled {
		return false, nil
	}
	if deps.Files == nil || deps.DirCache == nil {
		return false, nil
	}
	if runMode == domain.StrmRunModeBranch {
		return false, nil
	}
	if task.BranchCheckEnabled && runMode != domain.StrmRunModeFull {
		return false, nil
	}
	return deps.Files.SupportsFullList(ctx, task.AccountID)
}

// scanEnhancedTask 以 cur=0 全量清单替代逐目录递归：
// 拉清单 → pid→路径 翻译（缓存 + get_info 补漏）→ 构建候选 → 复用 finalizeScan 生成/同步/清理。
func scanEnhancedTask(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	root string,
	exts, metaExts map[string]struct{},
	excludeDirs, excludeFiles []string,
	minMediaBytes, metaMaxBytes int64,
	failures *FailureCollector,
) (ScanResult, error) {
	var result ScanResult
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	entries, err := deps.Files.ListAllFiles(ctx, task.AccountID, task.ParentID)
	if err != nil {
		return result, err
	}
	dirPaths, err := resolveDirPaths(ctx, deps, task.AccountID, entries)
	if err != nil {
		return result, err
	}
	if derr := pruneDirCache(ctx, deps, task, entries); derr != nil {
		log.Warn("strm dir cache prune failed", "account_id", task.AccountID, "err", derr.Error())
	}

	rootSegs := splitRemotePath(task.Path)
	outputFolder := TaskRelDir(task.GroupDir, task.OutputFolder)
	var candidates []mediaCandidate
	var metadataItems []metadataItem
	dirHasMedia := make(map[string]bool)
	subtreeHasMedia := make(map[string]bool)
	state := &branchScanState{
		skippedDirs:    make(map[string]struct{}),
		metadataDirs:   make(map[string]metadataDirectory),
		cleanupScopes:  []cleanupScope{{recursive: true}},
		remoteChildren: nil, // 清单不含空目录，禁用目录级清理避免误删
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if matchesKeywordRules(e.Name, excludeFiles) {
			continue
		}
		relDirs, ok := relDirsOf(dirPaths[e.ParentID], e.Name, rootSegs)
		if !ok {
			continue // 远端路径不在任务根范围内，忽略
		}
		recordMetadataDirectory(state.metadataDirs, e.ParentID, relDirs)
		classified := classifyScanFile(e.FileID, e.Name, outputFolder, e.Size, relDirs, exts, metaExts, minMediaBytes, metaMaxBytes, task.SyncMetadata)
		if classified.hasMedia {
			candidates = append(candidates, classified.media)
			dirHasMedia[dirKey(relDirs)] = true
			markSubtreeMedia(subtreeHasMedia, relDirs)
			continue
		}
		if classified.hasMetadata {
			metadataItems = append(metadataItems, classified.metadata)
		}
	}

	log.Info("strm enhanced scan", "task_id", task.ID, "task_name", task.Name,
		"account_id", task.AccountID, "remote_files", len(entries),
		"candidates", len(candidates), "mode", "full-list")

	return finalizeScan(ctx, task, deps, scanHarvest{
		candidates:      candidates,
		metadataItems:   metadataItems,
		state:           state,
		dirHasMedia:     dirHasMedia,
		subtreeHasMedia: subtreeHasMedia,
	}, false, exts, metaExts, minMediaBytes, metaMaxBytes, root, failures)
}

// pruneDirCache 清理“任务根范围内、本次清单未出现”的 pid→路径 记录：
// 目录被删除（含其所有子路径）后，下次增强扫描即被清除，不需要定时任务。
// 清单为空、任务根为网盘根、或本次清单覆盖的目录数相对缓存明显缩水时跳过：
// 空清单与缩水清单都可能来自 115 分页异常/限流导致的拉取不完整，误清映射只会放大路径漂移。
func pruneDirCache(ctx context.Context, deps ScanDeps, task *domain.StrmTask, entries []driver.FullListEntry) error {
	if deps.DirCache == nil || len(entries) == 0 {
		return nil
	}
	prefix := strings.Trim(strings.ReplaceAll(strings.TrimSpace(task.Path), "\\", "/"), "/")
	if prefix == "" {
		return nil
	}
	existing, err := deps.DirCache.ListByPathPrefix(ctx, task.AccountID, prefix)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if pid := strings.TrimSpace(e.ParentID); pid != "" {
			seen[pid] = struct{}{}
		}
	}
	// 规模保护：本次清单实际覆盖的目录数不足已有缓存的一半时，判定为拉取不完整，
	// 跳过清理，避免把未扫到的目录误判为“已删除”而清掉映射。
	if len(seen) > 0 && len(seen)*2 < len(existing) {
		return nil
	}
	var stale []string
	for _, rec := range existing {
		if _, ok := seen[rec.DirID]; !ok {
			stale = append(stale, rec.DirID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := deps.DirCache.DeleteByIDs(ctx, task.AccountID, stale); err != nil {
		return err
	}
	return nil
}

// resolveDirPaths 返回 pid→完整远端路径 映射：
// 优先查 SQLite 缓存，未命中的调驱动 ResolveDirPath 反查并落库。
func resolveDirPaths(ctx context.Context, deps ScanDeps, accountID int64, entries []driver.FullListEntry) (map[string]string, error) {
	out := make(map[string]string, 64)
	if deps.DirCache == nil || deps.Files == nil {
		return out, nil
	}
	seen := make(map[string]struct{}, 64)
	for _, e := range entries {
		pid := strings.TrimSpace(e.ParentID)
		if pid == "" || pid == "0" {
			out[pid] = ""
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}
	}
	if len(seen) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(seen))
	for pid := range seen {
		ids = append(ids, pid)
	}
	hit, err := deps.DirCache.GetBatch(ctx, accountID, ids)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, pid := range ids {
		if p, ok := hit[pid]; ok {
			out[pid] = p
		} else {
			missing = append(missing, pid)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	now := time.Now()
	var fresh []domain.StrmDirCacheEntry
	for _, pid := range missing {
		p, rerr := deps.Files.ResolveDirPath(ctx, accountID, pid)
		if rerr != nil {
			return nil, rerr
		}
		out[pid] = p
		fresh = append(fresh, domain.StrmDirCacheEntry{
			AccountID: accountID, DirID: pid, DirPath: p, LastSeenAt: now,
		})
	}
	if len(fresh) > 0 {
		if err := deps.DirCache.UpsertBatch(ctx, fresh); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// relDirsOf 把远端完整路径裁掉任务根前缀，得到本地相对目录。
// 文件直接在任务根下时返回空切片；远端路径不在任务根内时返回 false。
func relDirsOf(dirPath, fileName string, rootSegs []string) ([]string, bool) {
	segs := splitRemotePath(dirPath)
	if len(segs) < len(rootSegs) {
		return nil, false
	}
	for i := range rootSegs {
		if !strings.EqualFold(segs[i], rootSegs[i]) {
			return nil, false
		}
	}
	if strings.TrimSpace(fileName) == "" {
		return nil, false
	}
	return segs[len(rootSegs):], true
}

func splitRemotePath(p string) []string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
