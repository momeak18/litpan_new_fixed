// Package announcement 提供后台公告：从维护者托管的远端 JSON 拉取公告内容，
// 供管理后台打开时自动弹出。判重依据是公告里的 notice_version，
// 已读状态由 API 层写入 settings；拉取失败静默降级，不影响后台其它功能。
package announcement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultURL 是公告远端文件地址（维护者托管在文档站），固定写死，不提供配置入口。
const DefaultURL = "https://www.litepan.top/announcement.json"

// Section 是公告正文的一个小节（对齐文档站 important-notice 的 ### 分节）。
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Announcement 是解析后的公告内容。
type Announcement struct {
	// Version 是判重版本：JSON 的 notice_version（建议用日期字符串，如 2026-08-20）；
	// 缺失时回落内容哈希（内容变化即视为新版本）。
	Version string `json:"notice_version"`
	// Badge 顶部徽章文字，如「重要公告」；可空。
	Badge string `json:"badge"`
	// Title 弹窗标题（JSON dialog_title）；缺失时默认「公告」。
	Title string `json:"dialog_title"`
	// Banner 黄色警示区文字（JSON banner）；可空。
	Banner string `json:"banner"`
	// Special 特别说明区（JSON special）：排在 banner 之下、正文之上，仅按纯文本展示；可空。
	Special string `json:"special"`
	// Lead 开场引导段（支持多行，前端 pre-wrap）；可空。
	Lead string `json:"lead"`
	// Sections 分节正文列表；可空。
	Sections []Section `json:"issues"`
	// Footnote 保留字段（前端不再展示）；可空。
	Footnote string `json:"footnote"`
	// FetchedAt 本次内容拉取时间。
	FetchedAt time.Time `json:"fetched_at"`
}

const (
	fetchTimeout = 10 * time.Second
	cacheTTL     = 10 * time.Minute
	// failCooldown 拉取失败后的静默期，避免每次打开后台都打远端。
	failCooldown = 5 * time.Minute
	// maxBodyBytes 远端文件大小上限，防止异常大文件拖垮请求。
	maxBodyBytes = 512 * 1024
)

// Service 拉取并缓存公告。
type Service struct {
	url    string
	client *http.Client
	log    *slog.Logger

	mu       sync.Mutex
	cached   *Announcement
	cachedAt time.Time
	failedAt time.Time
}

// New 构造公告服务。url 为空（或全空白）时服务存在但始终无公告。
func New(url string, log *slog.Logger) *Service {
	return &Service{
		url:    strings.TrimSpace(url),
		client: &http.Client{Timeout: fetchTimeout},
		log:    log,
	}
}

// Enabled 返回公告服务是否配置了远端文件。
func (s *Service) Enabled() bool {
	return s.url != ""
}

// Fetch 返回当前公告。未启用时返回 nil, nil；拉取失败时返回上次成功缓存（可能为 nil），不返回错误，
// 保证公告不可用时后台完全无感。结果在 cacheTTL 内复用，失败后 failCooldown 内不重试。
func (s *Service) Fetch(ctx context.Context) (*Announcement, error) {
	if s.url == "" {
		return nil, nil
	}
	s.mu.Lock()
	now := time.Now()
	if s.cached != nil && now.Sub(s.cachedAt) < cacheTTL {
		item := *s.cached
		s.mu.Unlock()
		return &item, nil
	}
	if !s.failedAt.IsZero() && now.Sub(s.failedAt) < failCooldown {
		item := s.cached
		s.mu.Unlock()
		return item, nil
	}
	s.mu.Unlock()

	body, err := s.fetchBody(ctx)
	if err != nil {
		s.mu.Lock()
		s.failedAt = time.Now()
		s.mu.Unlock()
		if s.log != nil {
			s.log.Warn("announcement fetch failed", "url", s.url, "err", err)
		}
		s.mu.Lock()
		item := s.cached
		s.mu.Unlock()
		return item, nil
	}

	item := parse(body)
	if item == nil {
		s.mu.Lock()
		s.failedAt = time.Now()
		cached := s.cached
		s.mu.Unlock()
		if s.log != nil {
			s.log.Warn("announcement content ignored", "url", s.url, "reason", "invalid json or empty content")
		}
		return cached, nil
	}
	item.FetchedAt = time.Now()
	s.mu.Lock()
	s.cached = item
	s.cachedAt = item.FetchedAt
	s.failedAt = time.Time{}
	s.mu.Unlock()
	return item, nil
}

func (s *Service) fetchBody(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LitePan/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("announcement file exceeds %d bytes", maxBodyBytes)
	}
	return body, nil
}

// parse 只接受文档站约定的有效 JSON。格式异常、非 JSON 或没有可展示正文时返回 nil，
// 调用方静默沿用旧缓存或返回暂无公告，避免把错误页和损坏内容展示给用户。
func parse(raw []byte) *Announcement {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	hash := contentHash(text)
	if a, ok := parseJSON(raw, hash); ok {
		return &a
	}
	return nil
}

type jsonAnnouncement struct {
	Version  string    `json:"notice_version"`
	Badge    string    `json:"badge"`
	Title    string    `json:"dialog_title"`
	Banner   string    `json:"banner"`
	Special  string    `json:"special"`
	Lead     string    `json:"lead"`
	Issues   []Section `json:"issues"`
	Footnote string    `json:"footnote"`
}

func parseJSON(raw []byte, hash string) (Announcement, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return Announcement{}, false
	}
	var ja jsonAnnouncement
	if err := json.Unmarshal(raw, &ja); err != nil {
		return Announcement{}, false
	}
	if strings.TrimSpace(ja.Title) == "" && strings.TrimSpace(ja.Lead) == "" && len(ja.Issues) == 0 {
		return Announcement{}, false
	}
	version := strings.TrimSpace(ja.Version)
	if version == "" {
		version = hash
	}
	sections := make([]Section, 0, len(ja.Issues))
	for _, s := range ja.Issues {
		s.Title = strings.TrimSpace(s.Title)
		s.Body = strings.TrimSpace(s.Body)
		if s.Title == "" && s.Body == "" {
			continue
		}
		sections = append(sections, s)
	}
	title := strings.TrimSpace(ja.Title)
	if title == "" {
		title = "公告"
	}
	return Announcement{
		Version:  version,
		Badge:    normalizeVisible(ja.Badge),
		Title:    title,
		Banner:   normalizeVisible(ja.Banner),
		Special:  normalizeVisible(ja.Special),
		Lead:     normalizeVisible(ja.Lead),
		Sections: sections,
		Footnote: strings.TrimSpace(ja.Footnote),
	}, true
}

// normalizeVisible 归一化可选文本区（badge/banner/special/lead）：
// 空值、none、false（不区分大小写）一律视为不显示。
func normalizeVisible(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none", "false":
		return ""
	}
	return strings.TrimSpace(v)
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
