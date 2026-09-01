package upload

import (
	"context"
	"strings"

	"litepan/internal/domain"
)

func (m *Manager) Delete(ctx context.Context, taskID string, deleteUploadedFile bool) (bool, error) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	if deleteUploadedFile && st.Status == StatusSuccess {
		if err := m.deleteUploadedFile(ctx, st); err != nil {
			return true, err
		}
	}
	if err := m.stopTaskForDelete(ctx, taskID); err != nil {
		return true, err
	}
	popped := m.popTask(taskID)
	if popped == nil {
		return true, domain.Errorf(domain.CodeInternal, "任务正在停止，请稍后再次删除")
	}
	m.cleanupLocalSourceAfterDelete(popped)
	m.broadcast()
	return true, nil
}

func (m *Manager) BatchDelete(ctx context.Context, taskIDs []string, deleteUploadedFile bool) BatchDeleteResult {
	result := BatchDeleteResult{FailedMessages: map[string]string{}}
	seen := map[string]struct{}{}
	type item struct {
		id     string
		st     *taskState
		fileID string
	}
	var items []item
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		m.mu.Lock()
		st, ok := m.tasks[id]
		m.mu.Unlock()
		if !ok {
			result.MissingTaskIDs = append(result.MissingTaskIDs, id)
			continue
		}
		fileID := ""
		if deleteUploadedFile && st.Status == StatusSuccess && st.Result != nil {
			fileID, _ = st.Result["file_id"].(string)
		}
		items = append(items, item{id: id, st: st, fileID: strings.TrimSpace(fileID)})
	}
	if deleteUploadedFile {
		type groupKey struct {
			accountID int64
			parent    string
		}
		byGroup := map[groupKey][]string{}
		var order []groupKey
		for _, it := range items {
			if it.fileID == "" {
				continue
			}
			g := groupKey{accountID: it.st.AccountID, parent: it.st.TargetPath}
			if _, ok := byGroup[g]; !ok {
				order = append(order, g)
			}
			byGroup[g] = append(byGroup[g], it.fileID)
		}
		failedByID := map[string]string{}
		for _, g := range order {
			var err error
			if m.files != nil {
				err = m.files.DeleteFiles(ctx, g.accountID, byGroup[g], g.parent)
			} else {
				err = m.deleteUploadedFiles(ctx, g.accountID, byGroup[g])
			}
			if err != nil {
				for _, it := range items {
					if it.st.AccountID == g.accountID && it.st.TargetPath == g.parent && it.fileID != "" {
						failedByID[it.id] = err.Error()
					}
				}
			}
		}
		for id, msg := range failedByID {
			result.FailedTaskIDs = append(result.FailedTaskIDs, id)
			result.FailedMessages[id] = msg
		}
		for _, g := range order {
			if g.parent == "" || m.files == nil {
				continue
			}
			groupFailed := false
			for _, it := range items {
				if it.st.AccountID == g.accountID && it.st.TargetPath == g.parent && it.fileID != "" {
					if _, bad := failedByID[it.id]; bad {
						groupFailed = true
						break
					}
				}
			}
			if groupFailed {
				continue
			}
			entries, lerr := m.files.List(ctx, g.accountID, g.parent, false)
			if lerr != nil || len(entries) > 0 {
				continue
			}
			_ = m.files.DeleteFiles(ctx, g.accountID, []string{g.parent}, "")
		}
	}
	for _, it := range items {
		if _, failed := result.FailedMessages[it.id]; failed {
			continue
		}
		if err := m.stopTaskForDelete(ctx, it.id); err != nil {
			result.FailedTaskIDs = append(result.FailedTaskIDs, it.id)
			result.FailedMessages[it.id] = err.Error()
			continue
		}
		popped := m.popTask(it.id)
		if popped == nil {
			result.FailedTaskIDs = append(result.FailedTaskIDs, it.id)
			result.FailedMessages[it.id] = "任务正在停止，请稍后再次删除"
			continue
		}
		m.cleanupLocalSourceAfterDelete(popped)
		result.DeletedTaskIDs = append(result.DeletedTaskIDs, it.id)
	}
	if len(result.DeletedTaskIDs) > 0 {
		m.broadcast()
	}
	return result
}

// 删除任务时不碰服务器上传的用户源文件。
func (m *Manager) cleanupLocalSourceAfterDelete(st *taskState) {
	if st == nil || st.SourceType == SourceTypeServerLocal {
		return
	}
	if st.CleanupLocalMode != "" {
		m.cleanupLocalSource(st.localPath, st.CleanupLocalPath, st.CleanupLocalMode)
		return
	}
	m.removeLocalFile(st.localPath)
}
