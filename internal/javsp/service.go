// Package javsp runs JavSP as short-lived Docker jobs.
package javsp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
)

const (
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"

	defaultImage        = "apecme/javsp-web:bata"
	defaultContainerDir = "/video"
	maxLogBytes         = 256 * 1024
)

var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

type Config struct {
	Enabled           bool   `json:"enabled"`
	HostMediaDir      string `json:"host_media_dir"`
	ContainerMediaDir string `json:"container_media_dir"`
	Image             string `json:"image"`
	MemoryLimitMB     int    `json:"memory_limit_mb"`
	ConfigYAML        string `json:"config_yaml,omitempty"`
}

type Task struct {
	ID           string `json:"id"`
	RelativePath string `json:"relative_path"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	Error        string `json:"error,omitempty"`
	Log          string `json:"log,omitempty"`
	Container    string `json:"container,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type Options struct {
	DataDir string
	Log     *slog.Logger
}

type persisted struct {
	Config Config  `json:"config"`
	Tasks  []*Task `json:"tasks"`
}

type Service struct {
	root, state string
	log         *slog.Logger

	mu      sync.Mutex
	config  Config
	tasks   map[string]*Task
	cancels map[string]context.CancelFunc
	wake    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func New(opts Options) *Service {
	s := &Service{
		root: filepath.Join(opts.DataDir, "javsp"), state: filepath.Join(opts.DataDir, "javsp", "state.json"),
		log: opts.Log, tasks: map[string]*Task{}, cancels: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1),
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
	for _, task := range s.tasks {
		if task.Status == StatusRunning {
			task.Status, task.Message = StatusQueued, "Waiting to resume after service restart"
			/*
			task.Status, task.Message = StatusQueued, "鏈嶅姟閲嶅惎鍚庣瓑寰呴噸鏂版墽琛?
			*/
		}
	}
	s.persistLocked()
	s.mu.Unlock()
	s.wg.Add(1)
	go s.loop()
	s.signal()
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

func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizeConfig(s.config)
}

func (s *Service) SetConfig(cfg Config) (Config, error) {
	cfg = normalizeConfig(cfg)
	if cfg.Enabled && strings.TrimSpace(cfg.HostMediaDir) == "" {
		return Config{}, domain.Errorf(domain.CodeValidation, "The media directory must be an absolute host path")
		/*
		return Config{}, domain.Errorf(domain.CodeValidation, "璇峰～鍐?Docker 涓绘満涓婄殑濯掍綋鐩綍")
		*/
	}
	if !isAbsoluteHostPath(cfg.HostMediaDir) && cfg.HostMediaDir != "" {
		return Config{}, domain.Errorf(domain.CodeValidation, "The host media directory must be an absolute path")
		/*
		return Config{}, domain.Errorf(domain.CodeValidation, "濯掍綋鐩綍蹇呴』鏄?Docker 涓绘満鐨勭粷瀵硅矾寰?)
		*/
	}
	s.mu.Lock()
	s.config = cfg
	s.persistLocked()
	s.mu.Unlock()
	return cfg, nil
}

func (s *Service) Create(relativePath string) (*Task, error) {
	relativePath, err := cleanRelativePath(relativePath)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if !s.config.Enabled {
		s.mu.Unlock()
		return nil, domain.Errorf(domain.CodeValidation, "Enable JavSP scraping and save the media directory first")
		/*
		s.mu.Unlock()
		return nil, domain.Errorf(domain.CodeValidation, "璇峰厛鍚敤 JavSP 鎸夐渶鍒墛骞朵繚瀛樺獟浣撶洰褰?)
		*/
	}
	now := time.Now().Unix()
	t := &Task{ID: newID(), RelativePath: relativePath, Status: StatusQueued, Message: "Queued", CreatedAt: now, UpdatedAt: now}
	/*
	t := &Task{ID: newID(), RelativePath: relativePath, Status: StatusQueued, Message: "绛夊緟鎵ц", CreatedAt: now, UpdatedAt: now}
	*/
	s.tasks[t.ID] = t
	s.persistLocked()
	out := cloneTask(t)
	s.mu.Unlock()
	s.signal()
	return out, nil
}

func (s *Service) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		out = append(out, *cloneTask(task))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return domain.Errorf(domain.CodeNotFound, "JavSP task does not exist")
		/*
		return domain.Errorf(domain.CodeNotFound, "JavSP 浠诲姟涓嶅瓨鍦?)
		*/
	}
	if t.Status != StatusQueued && t.Status != StatusRunning {
		return nil
	}
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
	}
	t.Status, t.Message, t.UpdatedAt = StatusCanceled, "Cancellation requested", time.Now().Unix()
	/*
	t.Status, t.Message, t.UpdatedAt = StatusCanceled, "宸茶姹傚仠姝?, time.Now().Unix()
	*/
	s.persistLocked()
	return nil
}

func (s *Service) loop() {
	defer s.wg.Done()
	for {
		if s.runNext() {
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
	}
}

func (s *Service) runNext() bool {
	s.mu.Lock()
	var next *Task
	for _, task := range s.tasks {
		if task.Status == StatusQueued && (next == nil || task.CreatedAt < next.CreatedAt) {
			next = cloneTask(task)
		}
	}
	if next == nil {
		s.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancels[next.ID] = cancel
	s.tasks[next.ID].Status, s.tasks[next.ID].Message, s.tasks[next.ID].UpdatedAt = StatusRunning, "Starting JavSP container", time.Now().Unix()
	/*
	s.tasks[next.ID].Status, s.tasks[next.ID].Message, s.tasks[next.ID].UpdatedAt = StatusRunning, "姝ｅ湪鍚姩 JavSP 瀹瑰櫒", time.Now().Unix()
	*/
	s.persistLocked()
	s.mu.Unlock()
	err := s.run(ctx, next)
	canceled := ctx.Err() != nil
	cancel()
	s.mu.Lock()
	delete(s.cancels, next.ID)
	t := s.tasks[next.ID]
	if t != nil && t.Status != StatusCanceled {
		t.UpdatedAt = time.Now().Unix()
		if canceled {
			t.Status, t.Message = StatusCanceled, "Canceled"
			/*
			t.Status, t.Message = StatusCanceled, "宸插仠姝?
			*/
		} else if err != nil {
			t.Status, t.Message, t.Error = StatusFailed, "鍒墛澶辫触", err.Error()
		} else {
			t.Status, t.Message, t.Error = StatusSuccess, "JavSP task completed", ""
			/*
			t.Status, t.Message, t.Error = StatusSuccess, "鍒墛瀹屾垚锛屽鍣ㄥ凡鑷姩鍒犻櫎", ""
			*/
		}
		s.persistLocked()
	}
	s.mu.Unlock()
	return true
}

func (s *Service) run(ctx context.Context, task *Task) error {
	cfg := s.Config()
	container := "litepan-javsp-" + task.ID[:12]
	s.setContainer(task.ID, container)
	args := []string{"create", "--name", container, "-v", cfg.HostMediaDir + ":" + cfg.ContainerMediaDir + ":rw", "--entrypoint", "/app/.venv/bin/javsp"}
	if cfg.MemoryLimitMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", cfg.MemoryLimitMB))
	}
	args = append(args, cfg.Image)
	if _, err := docker(ctx, args...); err != nil {
		return fmt.Errorf("鍒涘缓 JavSP 瀹瑰櫒: %w", err)
	}
	defer func() { _, _ = docker(context.Background(), "rm", "-f", container) }()

	configPath := filepath.Join(s.root, "tasks", task.ID, "config.yml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.ConfigYAML) != "" {
		if err := os.WriteFile(configPath, []byte(cfg.ConfigYAML), 0o600); err != nil {
			return err
		}
	} else if _, err := docker(ctx, "cp", container+":/app/config.yml", configPath); err != nil {
		return fmt.Errorf("璇诲彇 JavSP 榛樿閰嶇疆: %w", err)
	}
	if err := patchConfig(configPath, filepath.ToSlash(filepath.Join(cfg.ContainerMediaDir, task.RelativePath))); err != nil {
		return err
	}
	if _, err := docker(ctx, "cp", configPath, container+":/app/config.yml"); err != nil {
		return fmt.Errorf("鍐欏叆浠诲姟閰嶇疆: %w", err)
	}
	s.setMessage(task.ID, "JavSP 姝ｅ湪鍒墛")
	out, err := docker(ctx, "start", "-a", container)
	s.setLog(task.ID, out)
	if err != nil {
		return fmt.Errorf("JavSP 鎵ц澶辫触: %w", err)
	}
	return nil
}

func patchConfig(path, input string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(b)
	text, err = replaceConfigLine(text, "input_directory", fmt.Sprintf("input_directory: %q", input))
	if err != nil {
		return err
	}
	text, _ = replaceConfigLine(text, "manual", "manual: false")
	text, _ = replaceConfigLine(text, "interactive", "interactive: false")
	text, _ = replaceConfigLine(text, "move_files", "move_files: false")
	return os.WriteFile(path, []byte(text), 0o600)
}

func replaceConfigLine(text, key, replacement string) (string, error) {
	re := regexp.MustCompile(`(?m)^([ \t]*)` + regexp.QuoteMeta(key) + `:[^\r\n]*`)
	if !re.MatchString(text) {
		return text, fmt.Errorf("JavSP 閰嶇疆缂哄皯 %s", key)
	}
	return re.ReplaceAllStringFunc(text, func(match string) string {
		return re.FindStringSubmatch(match)[1] + replacement
	}), nil
}

func normalizeConfig(cfg Config) Config {
	cfg.HostMediaDir = strings.TrimSpace(cfg.HostMediaDir)
	cfg.ContainerMediaDir = strings.TrimSpace(cfg.ContainerMediaDir)
	if cfg.ContainerMediaDir == "" {
		cfg.ContainerMediaDir = defaultContainerDir
	}
	cfg.ContainerMediaDir = filepath.ToSlash(cfg.ContainerMediaDir)
	cfg.Image = strings.TrimSpace(cfg.Image)
	if cfg.Image == "" {
		cfg.Image = defaultImage
	}
	if cfg.MemoryLimitMB < 0 {
		cfg.MemoryLimitMB = 0
	}
	if cfg.MemoryLimitMB > 4096 {
		cfg.MemoryLimitMB = 4096
	}
	return cfg
}

func cleanRelativePath(raw string) (string, error) {
	raw = strings.Trim(strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/")), "/")
	if raw == "" {
		return ".", nil
	}
	if strings.Contains(raw, "../") || raw == ".." || strings.HasPrefix(raw, "/") {
		return "", domain.Errorf(domain.CodeValidation, "The scrape directory must be inside the configured media directory")
		/*
		return "", domain.Errorf(domain.CodeValidation, "鍒墛鐩綍鍙兘鏄獟浣撶洰褰曚笅鐨勭浉瀵硅矾寰?)
		*/
	}
	return raw, nil
}

func isAbsoluteHostPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return windowsAbsolutePath.MatchString(path)
}

func (s *Service) setContainer(id, container string) {
	s.mu.Lock()
	if t := s.tasks[id]; t != nil {
		t.Container = container
		t.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
	s.mu.Unlock()
}
func (s *Service) setMessage(id, message string) {
	s.mu.Lock()
	if t := s.tasks[id]; t != nil {
		t.Message = message
		t.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
	s.mu.Unlock()
}
func (s *Service) setLog(id, log string) {
	s.mu.Lock()
	if t := s.tasks[id]; t != nil {
		if len(log) > maxLogBytes {
			log = log[len(log)-maxLogBytes:]
		}
		t.Log = log
		t.UpdatedAt = time.Now().Unix()
		s.persistLocked()
	}
	s.mu.Unlock()
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) persistLocked() {
	_ = os.MkdirAll(s.root, 0o755)
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, cloneTask(t))
	}
	b, err := json.MarshalIndent(persisted{Config: s.config, Tasks: tasks}, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.state, b, 0o600)
	}
}
func (s *Service) restore() {
	b, err := os.ReadFile(s.state)
	if err != nil {
		return
	}
	var p persisted
	if json.Unmarshal(b, &p) != nil {
		return
	}
	s.config = normalizeConfig(p.Config)
	for _, t := range p.Tasks {
		if t != nil && t.ID != "" {
			s.tasks[t.ID] = t
		}
	}
}
func cloneTask(in *Task) *Task {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

