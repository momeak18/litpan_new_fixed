// Package localextract implements the intentionally small, local archive queue.
// It never calls a provider cloud-extract API: archives are downloaded to the
// LitePan host, expanded with 7-Zip, then uploaded through LitePan's normal
// resumable upload manager.
package localextract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/file"
	"litepan/internal/playback"
	"litepan/internal/upload"
)

const (
	StatusQueued      = "queued"
	StatusDownloading = "downloading"
	StatusExtracting  = "extracting"
	StatusUploading   = "uploading"
	StatusSuccess     = "success"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
)

const localExtractWorkers = 3

var errArchivePasswordRequired = errors.New("压缩包需要密码，已跳过")

type Task struct {
	TaskID            string   `json:"task_id"`
	AccountID         int64    `json:"account_id"`
	SourceFileID      string   `json:"source_file_id"`
	SourceFileName    string   `json:"source_file_name"`
	TargetParentID    string   `json:"target_parent_id"`
	TargetDisplayPath string   `json:"target_display_path"`
	Status            string   `json:"status"`
	Progress          int      `json:"progress"`
	Message           string   `json:"message"`
	Error             string   `json:"error,omitempty"`
	UploadedFiles     int      `json:"uploaded_files"`
	TotalFiles        int      `json:"total_files"`
	UploadTaskIDs     []string `json:"upload_task_ids,omitempty"`
	RetryCount        int      `json:"retry_count,omitempty"`
	ReuseArchive      bool     `json:"reuse_archive,omitempty"`
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

type CreateParams struct {
	AccountID         int64
	SourceFileID      string
	SourceFileName    string
	TargetParentID    string
	TargetDisplayPath string
}

type Options struct {
	DataDir  string
	Playback *playback.Service
	Files    *file.Service
	Uploads  *upload.Manager
	Log      *slog.Logger
}

type Service struct {
	dataDir  string
	rootDir  string
	state    string
	playback *playback.Service
	files    *file.Service
	uploads  *upload.Manager
	log      *slog.Logger

	mu      sync.Mutex
	tasks   map[string]*Task
	cancels map[string]context.CancelFunc
	running map[string]struct{}
	wake    chan struct{}
	extract chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(opts Options) *Service {
	root := filepath.Join(opts.DataDir, "local-extract")
	s := &Service{
		dataDir: opts.DataDir, rootDir: root, state: filepath.Join(root, "tasks.json"),
		playback: opts.Playback, files: opts.Files, uploads: opts.Uploads,
		log: opts.Log, tasks: map[string]*Task{}, cancels: map[string]context.CancelFunc{}, running: map[string]struct{}{},
		wake: make(chan struct{}, localExtractWorkers), extract: make(chan struct{}, 1),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.restore()
	return s
}

func (s *Service) Start(parent context.Context) {
	s.mu.Lock()
	if s.ctx != nil {
		s.mu.Unlock()
		return
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.mu.Unlock()
	if token := strings.TrimSpace(os.Getenv("LITEPAN_LOCAL_EXTRACT_RETRY_FAILED_TOKEN")); token != "" {
		retried, skipped := s.retryFailedOnce(token)
		s.log.Info("本地解压失败任务自动重试", "retried", retried, "password_skipped", skipped)
	}
	for i := 0; i < localExtractWorkers; i++ {
		s.wg.Add(1)
		go s.loop()
	}
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Create(ctx context.Context, p CreateParams) (*Task, error) {
	if p.AccountID <= 0 || strings.TrimSpace(p.SourceFileID) == "" || strings.TrimSpace(p.SourceFileName) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "本地解压任务参数不完整")
	}
	if !isArchive(p.SourceFileName) {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持 .zip、.rar 和 .7z 压缩包")
	}
	if s.playback == nil || s.files == nil || s.uploads == nil {
		return nil, domain.Errorf(domain.CodeInternal, "本地解压服务未就绪")
	}
	now := time.Now().Unix()
	t := &Task{TaskID: newID(), AccountID: p.AccountID, SourceFileID: strings.TrimSpace(p.SourceFileID), SourceFileName: filepath.Base(strings.TrimSpace(p.SourceFileName)), TargetParentID: strings.TrimSpace(p.TargetParentID), TargetDisplayPath: strings.TrimSpace(p.TargetDisplayPath), Status: StatusQueued, Message: "等待本地下载", CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	s.tasks[t.TaskID] = t
	s.persistLocked()
	out := clone(t)
	s.mu.Unlock()
	s.signal()
	return out, nil
}

func (s *Service) List(_ context.Context, accountID int64) []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if accountID == 0 || task.AccountID == accountID {
			out = append(out, *clone(task))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].TaskID < out[j].TaskID
	})
	return out
}

func (s *Service) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return domain.Errorf(domain.CodeNotFound, "本地解压任务不存在")
	}
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
	}
	delete(s.tasks, id)
	s.persistLocked()
	s.mu.Unlock()
	if t.Status != StatusSuccess {
		_ = os.RemoveAll(s.taskDir(id))
	}
	return nil
}

func (s *Service) loop() {
	defer s.wg.Done()
	for {
		if !s.runNext() {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			}
		}
	}
}

func (s *Service) runNext() bool {
	s.mu.Lock()
	var next *Task
	for _, t := range s.tasks {
		if t.Status == StatusQueued || t.Status == StatusDownloading || t.Status == StatusExtracting || t.Status == StatusUploading {
			if _, busy := s.running[t.TaskID]; busy {
				continue
			}
			if next == nil || t.CreatedAt < next.CreatedAt || (t.CreatedAt == next.CreatedAt && t.TaskID < next.TaskID) {
				next = clone(t)
			}
		}
	}
	if next == nil {
		s.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancels[next.TaskID] = cancel
	s.running[next.TaskID] = struct{}{}
	s.mu.Unlock()
	s.run(ctx, next.TaskID)
	s.mu.Lock()
	delete(s.cancels, next.TaskID)
	delete(s.running, next.TaskID)
	s.mu.Unlock()
	return true
}

func (s *Service) run(ctx context.Context, id string) {
	t, ok := s.get(id)
	if !ok {
		return
	}
	if len(t.UploadTaskIDs) > 0 && t.Status == StatusUploading {
		s.waitUploads(ctx, t)
		return
	}
	base := s.taskDir(id)
	archive := filepath.Join(base, "source"+filepath.Ext(t.SourceFileName))
	// Every extraction attempt gets a fresh directory. Failed 7-Zip attempts can
	// leave partial files behind, and Windows bind mounts do not always remove
	// those trees synchronously before unrar starts.
	outDir := filepath.Join(base, fmt.Sprintf("output-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(base, 0o755); err != nil {
		s.fail(id, err)
		return
	}
	if t.ReuseArchive {
		if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
			t.ReuseArchive = false
		} else if err := s.set(id, StatusExtracting, 30, "正在重新处理已下载的压缩包", ""); err != nil {
			return
		}
	}
	if !t.ReuseArchive {
		if err := s.set(id, StatusDownloading, 5, "正在下载压缩包", ""); err != nil {
			return
		}
		if err := s.download(ctx, t, archive); err != nil {
			s.failIfActive(id, err)
			return
		}
	}
	if err := s.set(id, StatusExtracting, 35, "正在用 7-Zip 解压", ""); err != nil {
		return
	}
	select {
	case s.extract <- struct{}{}:
	case <-ctx.Done():
		s.cancelIfActive(id)
		return
	}
	_ = os.RemoveAll(outDir)
	extractErr := extractArchive(ctx, archive, outDir)
	<-s.extract
	if extractErr != nil {
		s.failIfActive(id, extractErr)
		return
	}
	paths, err := regularFiles(outDir)
	if err != nil {
		s.failIfActive(id, err)
		return
	}
	if len(paths) == 0 {
		s.failIfActive(id, fmt.Errorf("压缩包内没有可上传的文件"))
		return
	}
	if err := s.set(id, StatusUploading, 55, "正在创建上传队列", ""); err != nil {
		return
	}
	rootName := strings.TrimSuffix(t.SourceFileName, filepath.Ext(t.SourceFileName))
	remoteRoot, err := s.ensureDir(ctx, t.AccountID, t.TargetParentID, rootName)
	if err != nil {
		s.failIfActive(id, err)
		return
	}
	params := make([]upload.ServerLocalCreateParams, 0, len(paths))
	for _, local := range paths {
		rel, _ := filepath.Rel(outDir, local)
		parent, err := s.ensureNestedDir(ctx, t.AccountID, remoteRoot, filepath.Dir(rel))
		if err != nil {
			s.failIfActive(id, err)
			return
		}
		info, statErr := os.Stat(local)
		if statErr != nil {
			s.failIfActive(id, statErr)
			return
		}
		params = append(params, upload.ServerLocalCreateParams{ClientTaskID: "local-extract:" + id + ":" + filepath.ToSlash(rel), AccountID: t.AccountID, FileName: filepath.Base(local), SourceType: upload.SourceTypeServerLocal, TargetPath: parent, TargetDisplayPath: strings.TrimRight(t.TargetDisplayPath, "/") + "/" + rootName, LocalPath: local, TotalBytes: info.Size(), ConflictPolicy: "skip", CleanupLocalMode: upload.CleanupLocalModeKeep})
	}
	created, err := s.uploads.CreateServerLocalTasks(ctx, params)
	if err != nil {
		s.failIfActive(id, err)
		return
	}
	ids := make([]string, 0, len(created))
	for _, item := range created {
		if item != nil {
			ids = append(ids, item.TaskID)
		}
	}
	s.mu.Lock()
	if current := s.tasks[id]; current != nil {
		current.UploadTaskIDs = ids
		current.TotalFiles = len(ids)
		current.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
	s.mu.Unlock()
	s.waitUploads(ctx, t)
}

func (s *Service) waitUploads(ctx context.Context, initial *Task) {
	id := initial.TaskID
	for {
		t, ok := s.get(id)
		if !ok {
			return
		}
		complete, failed := 0, ""
		for _, uploadID := range t.UploadTaskIDs {
			up, exists := s.uploads.Get(ctx, uploadID)
			if !exists {
				failed = "上传子任务不存在"
				break
			}
			switch up.Status {
			case upload.StatusSuccess, upload.StatusSkipped:
				complete++
			case upload.StatusFailed, upload.StatusCanceled:
				failed = up.Error
				if failed == "" {
					failed = up.Message
				}
			}
		}
		if failed != "" {
			s.failIfActive(id, fmt.Errorf("上传失败: %s", failed))
			return
		}
		if len(t.UploadTaskIDs) > 0 && complete == len(t.UploadTaskIDs) {
			s.mu.Lock()
			if current := s.tasks[id]; current != nil {
				current.Status = StatusSuccess
				current.Progress = 100
				current.UploadedFiles = complete
				current.Message = "已完成，已清理本地临时文件"
				current.Error = ""
				current.UpdatedAt = time.Now().Unix()
				s.persistLocked()
			}
			s.mu.Unlock()
			_ = os.RemoveAll(s.taskDir(id))
			return
		}
		s.mu.Lock()
		if current := s.tasks[id]; current != nil {
			current.Status = StatusUploading
			current.Progress = 55 + int(float64(complete)/float64(max(1, len(current.UploadTaskIDs)))*44)
			current.UploadedFiles = complete
			current.Message = fmt.Sprintf("正在上传 %d/%d 个文件", complete, len(current.UploadTaskIDs))
			current.UpdatedAt = time.Now().Unix()
			s.persistLocked()
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.cancelIfActive(id)
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func (s *Service) download(ctx context.Context, t *Task, dest string) error {
	res, err := s.playback.Resolve(ctx, t.AccountID, t.SourceFileID, "LitePan/local-extract", true, false)
	if err != nil {
		return err
	}
	if res.File.IsDir {
		return domain.Errorf(domain.CodeValidation, "不能解压目录")
	}
	if res.Link.LocalPath != "" {
		in, err := os.Open(res.Link.LocalPath)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	size := res.File.Size
	if size <= 0 {
		size = res.Link.Size
	}
	return downloadSingle(ctx, res.Link.URL, res.Link.Headers, dest, size)
}

func extractArchive(ctx context.Context, archive, outDir string) error {
	var unrarProblem string
	unrarVerified := false
	if strings.EqualFold(filepath.Ext(archive), ".rar") {
		if unrarPath, err := exec.LookPath("unrar"); err == nil {
			testCmd := exec.CommandContext(ctx, unrarPath, "t", "-idq", "-p-", archive)
			testCmd.Stdin = strings.NewReader("")
			testOutput, testErr := testCmd.CombinedOutput()
			if archiveNeedsPassword(string(testOutput)) {
				return errArchivePasswordRequired
			}
			unrarVerified = testErr == nil

			cmd := exec.CommandContext(ctx, unrarPath, "x", "-idq", "-o+", "-p-", archive, filepath.Clean(outDir)+string(os.PathSeparator))
			cmd.Stdin = strings.NewReader("")
			output, extractErr := cmd.CombinedOutput()
			if extractErr == nil {
				return nil
			}
			if archiveNeedsPassword(string(output)) {
				return errArchivePasswordRequired
			}
			unrarProblem = compactToolOutput(string(output))
			_ = os.RemoveAll(outDir)
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "7z", "x", "-y", "-p", "-o"+outDir, archive)
	cmd.Stdin = strings.NewReader("")
	output, extractErr := cmd.CombinedOutput()
	if extractErr == nil {
		return nil
	}
	if archiveNeedsPassword(string(output)) {
		return errArchivePasswordRequired
	}
	if unrarVerified && isHarmlessRARHeaderError(string(output)) {
		if paths, err := regularFiles(outDir); err == nil && len(paths) > 0 {
			return nil
		}
	}
	sevenZipProblem := compactToolOutput(string(output))
	if unrarProblem != "" {
		return fmt.Errorf("RAR 解压失败；unrar: %s；7-Zip 回退: %s", unrarProblem, sevenZipProblem)
	}
	return fmt.Errorf("7-Zip 解压失败: %s", sevenZipProblem)
}

func isHarmlessRARHeaderError(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "headers error") {
		return false
	}
	for _, fatal := range []string{"data error", "crc failed", "unexpected end", "cannot open output file", "file name too long", "errno=36"} {
		if strings.Contains(lower, fatal) {
			return false
		}
	}
	return true
}

func archiveNeedsPassword(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{"enter password", "wrong password", "incorrect password", "password is incorrect", "encrypted archive", "archive is encrypted", "encrypted file", "需要密码", "密码错误"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func compactToolOutput(output string) string {
	output = strings.TrimSpace(output)
	const limit = 4000
	if len(output) <= limit {
		return output
	}
	return output[:2000] + "\n... 工具输出已截断 ...\n" + output[len(output)-2000:]
}

func downloadSingle(ctx context.Context, rawURL string, headers http.Header, dest string, expectedSize int64) error {
	resp, err := doDownloadRequest(ctx, rawURL, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("下载压缩包失败: HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("下载不完整：预期 %d 字节，实际 %d 字节", expectedSize, written)
	}
	return nil
}

func doDownloadRequest(ctx context.Context, rawURL string, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	return (&http.Client{Timeout: 0}).Do(req)
}

func (s *Service) ensureNestedDir(ctx context.Context, accountID int64, parent, rel string) (string, error) {
	if rel == "." || rel == "" {
		return parent, nil
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		var err error
		parent, err = s.ensureDir(ctx, accountID, parent, part)
		if err != nil {
			return "", err
		}
	}
	return parent, nil
}

func (s *Service) ensureDir(ctx context.Context, accountID int64, parent, name string) (string, error) {
	items, err := s.files.List(ctx, accountID, parent, false)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.IsDir && item.Name == name {
			return item.ID, nil
		}
	}
	created, err := s.files.CreateFolder(ctx, accountID, parent, name)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (s *Service) set(id, status string, progress int, message, problem string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil {
		return context.Canceled
	}
	t.Status = status
	t.Progress = progress
	t.Message = message
	t.Error = problem
	t.UpdatedAt = time.Now().Unix()
	s.persistLocked()
	return nil
}
func (s *Service) fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tasks[id]; t != nil {
		t.Status = StatusFailed
		t.Error = err.Error()
		t.Message = "任务失败，临时文件已保留"
		t.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
}
func (s *Service) failIfActive(id string, err error) {
	if err != context.Canceled {
		s.fail(id, err)
	}
}
func (s *Service) cancelIfActive(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tasks[id]; t != nil && t.Status != StatusSuccess {
		t.Status = StatusCanceled
		t.Message = "任务已停止，临时文件已保留"
		t.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
}
func (s *Service) get(id string) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return clone(t), ok
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) taskDir(id string) string { return filepath.Join(s.rootDir, "work", id) }

func (s *Service) retryFailedOnce(token string) (retried, passwordSkipped int) {
	marker := filepath.Join(s.rootDir, "retry-failed-token")
	if raw, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(raw)) == token {
		return 0, 0
	}
	now := time.Now().Unix()
	s.mu.Lock()
	for _, task := range s.tasks {
		if task == nil || (task.Status != StatusFailed && task.Status != StatusCanceled) {
			continue
		}
		if archiveNeedsPassword(task.Error) || strings.Contains(task.Error, errArchivePasswordRequired.Error()) {
			passwordSkipped++
			continue
		}
		archive := filepath.Join(s.taskDir(task.TaskID), "source"+filepath.Ext(task.SourceFileName))
		_, statErr := os.Stat(archive)
		task.ReuseArchive = task.Progress >= 30 && statErr == nil
		task.Status = StatusQueued
		task.Progress = 0
		task.Message = "等待重新处理失败任务"
		task.Error = ""
		task.UploadedFiles = 0
		task.TotalFiles = 0
		task.UploadTaskIDs = nil
		task.RetryCount++
		task.UpdatedAt = now
		retried++
	}
	if retried > 0 {
		s.persistLocked()
	}
	s.mu.Unlock()
	if err := os.WriteFile(marker, []byte(token+"\n"), 0o600); err != nil {
		s.log.Warn("写入失败任务重试标记失败", "err", err)
	}
	return retried, passwordSkipped
}

func (s *Service) restore() {
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		s.log.Warn("创建本地解压目录失败", "err", err)
		return
	}
	raw, err := os.ReadFile(s.state)
	if err != nil {
		return
	}
	var tasks []*Task
	if json.Unmarshal(raw, &tasks) != nil {
		s.log.Warn("读取本地解压队列失败")
		return
	}
	for _, t := range tasks {
		if t == nil || t.TaskID == "" {
			continue
		}
		if t.Status == StatusDownloading || t.Status == StatusExtracting {
			t.Status = StatusQueued
			t.Message = "服务重启后等待重新下载"
		}
		s.tasks[t.TaskID] = t
	}
	if len(s.tasks) > 0 {
		s.mu.Lock()
		s.persistLocked()
		s.mu.Unlock()
	}
}
func (s *Service) persistLocked() {
	_ = os.MkdirAll(s.rootDir, 0o755)
	all := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		all = append(all, clone(t))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt < all[j].CreatedAt })
	raw, err := json.MarshalIndent(all, "", "  ")
	if err == nil {
		tmp := s.state + ".tmp"
		if writeErr := os.WriteFile(tmp, raw, 0o600); writeErr == nil {
			_ = os.Rename(tmp, s.state)
		}
	}
}
func clone(t *Task) *Task {
	if t == nil {
		return nil
	}
	c := *t
	c.UploadTaskIDs = append([]string(nil), t.UploadTaskIDs...)
	return &c
}
func regularFiles(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}
func isArchive(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".rar", ".7z":
		return true
	}
	return false
}
func newID() string { var b [8]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
