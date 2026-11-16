package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"skillmanager/locales"
)

// 读取 GitHub 来源时用到的 skills.sh 接口（都没写进官方文档，随时可能变）：
//
//   - /api/download/{owner}/{repo}/{slug}：一个技能的全部文件内容。只用来
//     少发请求，内容逐个按 GitHub 目录树里的 blob 哈希核对，核对不过的文件
//     改从 GitHub 下载。
//   - /tele/audit?source=&skills=：ath、Socket、Snyk 的风险等级。取不到
//     不阻止添加，也不报错。
//
// 不调用 /tele/t，不上报安装数。

// ImportRemote 是读取 GitHub 来源要用的上游。测试里换成假的。
type ImportRemote interface {
	Remote
	// Snapshot 是 skills.sh 上这个技能的文件快照；取不到时返回错误，调用方退回 GitHub。
	Snapshot(ctx context.Context, repo, slug string) ([]SnapshotFile, error)
	// Audit 是 skills.sh 的安全审计，按技能名分组；取不到时返回错误。
	Audit(ctx context.Context, repo string, skills []string) (map[string][]AuditResult, error)
}

// SnapshotFile 是快照里的一个文件，Path 相对技能目录。
type SnapshotFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// AuditResult 是一家的审计结果。
type AuditResult struct {
	Provider string    // ath、socket、snyk
	Risk     string    // safe、low、medium、high、critical
	At       time.Time // 分析时间
	Alerts   int       // 告警条数（Socket 给）
}

// Risky 报告这家给的是 high 或 critical。
func (r AuditResult) Risky() bool { return r.Risk == "high" || r.Risk == "critical" }

// ProviderName 是给人看的审计方名称。
func (r AuditResult) ProviderName() string {
	switch r.Provider {
	case "ath":
		return "Gen Agent Trust Hub"
	case "socket":
		return "Socket"
	case "snyk":
		return "Snyk"
	}
	return r.Provider
}

// auditTimeout 是安全审计最多等多久：超时就不显示。
const auditTimeout = 3 * time.Second

// SkillsSh 是 skills.sh 的客户端。
type SkillsSh struct {
	Base, Audits string
	Client       *http.Client
}

// ImportClient 是 GitHub 加 skills.sh，Web 用来读网站来源。
type ImportClient struct {
	*GitHub
	Sh  *SkillsSh
	Web *http.Client
}

// NewImportRemote 建读取用的上游：代理、gh 登录状态和「更新」用同一套设置。
func NewImportRemote(s Settings) *ImportClient {
	g := NewUpdateRemote(s)
	return &ImportClient{GitHub: g, Sh: &SkillsSh{
		Base:   "https://skills.sh",
		Audits: "https://www.skills.sh",
		Client: &http.Client{Transport: g.Client.Transport, Timeout: requestTimeout},
	}, Web: &http.Client{Transport: g.Client.Transport, Timeout: requestTimeout}}
}

func (c *ImportClient) Snapshot(ctx context.Context, repo, slug string) ([]SnapshotFile, error) {
	return c.Sh.Snapshot(ctx, repo, slug)
}

func (c *ImportClient) Audit(ctx context.Context, repo string, skills []string) (map[string][]AuditResult, error) {
	return c.Sh.Audit(ctx, repo, skills)
}

// maxSnapshotBytes 是快照响应的上限：比单个技能的上限稍大。
const maxSnapshotBytes = maxImportBytes + 4<<20

func (s *SkillsSh) Snapshot(ctx context.Context, repo, slug string) ([]SnapshotFile, error) {
	owner, name, ok := strings.Cut(strings.ToLower(repo), "/")
	if !ok || slug == "" {
		return nil, errors.New(locales.ImportInvalidSnapshotParameters())
	}
	u := s.Base + "/api/download/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/" + url.PathEscape(slug)
	var body struct {
		Files []SnapshotFile `json:"files"`
	}
	if err := s.getJSON(ctx, u, maxSnapshotBytes, &body); err != nil {
		return nil, err
	}
	if len(body.Files) == 0 {
		return nil, errors.New(locales.ImportEmptySnapshot())
	}
	return body.Files, nil
}

func (s *SkillsSh) Audit(ctx context.Context, repo string, skills []string) (map[string][]AuditResult, error) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	q := url.Values{}
	q.Set("source", repo)
	q.Set("skills", strings.Join(skills, ","))
	var body map[string]map[string]struct {
		Risk       string `json:"risk"`
		AnalyzedAt string `json:"analyzedAt"`
		Alerts     int    `json:"alerts"`
	}
	if err := s.getJSON(ctx, s.Audits+"/tele/audit?"+q.Encode(), 1<<20, &body); err != nil {
		return nil, err
	}
	out := map[string][]AuditResult{}
	for skill, providers := range body {
		for _, p := range []string{"ath", "socket", "snyk"} {
			r, ok := providers[p]
			if !ok || r.Risk == "" {
				continue
			}
			at, _ := time.Parse(time.RFC3339Nano, r.AnalyzedAt)
			out[skill] = append(out[skill], AuditResult{Provider: p, Risk: strings.ToLower(r.Risk), At: at, Alerts: r.Alerts})
		}
	}
	return out, nil
}

func (s *SkillsSh) getJSON(ctx context.Context, u string, limit int64, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "skill-manager")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(v)
}
