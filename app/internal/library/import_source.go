package library

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"skillmanager/locales"
)

// 「添加技能」的输入：输入框、拖入、剪贴板都走 ParseImportInput。写法和
// skills@1.8.1 的 parseSource 一致，只有带点的短名字不同：GitHub 用户名
// 不能含点，所以 mintlify.com/mintlify 一定不是 GitHub 仓库，当成网站。

// ImportKind 是来源类型。
type ImportKind int

const (
	ImportGitHub      ImportKind = iota + 1
	ImportWebsite                // 网站来源（well-known）
	ImportLocal                  // 本机文件夹或 zip
	ImportUnsupported            // GitLab、其他 git 地址等
	ImportClawHub                // ClawHub 市场技能
)

// ImportSource 是识别出的来源。
type ImportSource struct {
	Kind    ImportKind
	Input   string // 去掉首尾空白和引号后的原文
	Repo    string // GitHub：owner/repo
	Ref     string // GitHub：用户指定的分支、标签或提交，没指定为空
	Subpath string // GitHub：仓库里的子目录；网站短名字：域名后面的部分
	URL     string // 网站来源的地址
	Paths   []string
	// Skills 是指定的技能（@skill、--skill、skills.sh 链接里的）：只读这几个
	// 并预选；空是读全部、不预选。
	Skills []string
	// All：指定了 Skills 也读整个仓库，仍预选 Skills（「显示全部」）。
	All bool
	// IgnoredArgs：粘贴的命令里还有 -g、-y、-a 等参数，这里不生效。
	IgnoredArgs bool
	// Guessed：网站地址是从带点的短名字猜的（mintlify.com/mintlify →
	// https://mintlify.com），找不到索引时提示粘贴完整命令。
	Guessed bool
}

// Label 是给人看的来源：owner/repo（带分支时 owner/repo#ref）、网址或路径。
func (s ImportSource) Label() string {
	switch s.Kind {
	case ImportGitHub:
		l := s.Repo
		if s.Subpath != "" {
			l += "/" + s.Subpath
		}
		return l
	case ImportWebsite:
		return strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://")
	case ImportClawHub:
		return s.Repo
	case ImportLocal:
		if len(s.Paths) == 1 {
			return filepath.Base(s.Paths[0])
		}
	}
	return s.Input
}

var (
	// ErrImportEmpty：输入框是空的。
	ErrImportEmpty = textErr(locales.ImportEmpty)
	// ErrImportUnknown：认不出这种写法。
	ErrImportUnknown = textErr(locales.ImportUnknownSource)
)

// wellKnownSkillURL 拆出 /.well-known/(agent-skills|skills)/ 前面的地址和后面的技能名。
var wellKnownSkillURL = regexp.MustCompile(`^(.*?)/\.well-known/(?:agent-skills|skills)(?:/([^/]+))?(?:/.*)?$`)

// GitHub 用户名：字母数字和连字符；仓库名还能有点和下划线。
// ClawHub 用户名和 slug 允许字母数字、点、下划线和连字符。
var (
	ghOwner      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	ghRepo       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	clawHubIdent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// ParseImportInput 识别一段输入。认不出时返回 ErrImportUnknown；能认出
// 但读不了的来源（GitLab、其他 git 地址）返回 Kind 为 ImportUnsupported 的结果。
func ParseImportInput(text string) (ImportSource, error) {
	in := trimQuotes(strings.TrimSpace(text))
	if in == "" {
		return ImportSource{}, ErrImportEmpty
	}
	if args, ok := installCommand(in); ok {
		src, skills, ignored, err := commandArgs(args)
		if err != nil {
			return ImportSource{}, err
		}
		out, err := ParseImportInput(src)
		if err != nil {
			return out, err
		}
		out.Input = in
		out.Skills = appendSkills(out.Skills, skills...)
		out.IgnoredArgs = ignored
		return out, nil
	}
	out := ImportSource{Input: in}
	switch {
	case isLocalPath(in):
		out.Kind, out.Paths = ImportLocal, []string{localPath(in)}
		return out, nil
	case strings.HasPrefix(in, "git@") || strings.HasPrefix(in, "ssh://") || strings.HasSuffix(in, ".git") && !strings.Contains(in, "github.com"):
		out.Kind = ImportUnsupported
		return out, nil
	}
	if u, ok := webURL(in); ok {
		return parseWebURL(out, u)
	}
	return parseShorthand(out, in)
}

// trimQuotes 去掉成对的首尾引号。
func trimQuotes(s string) string {
	for len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'' || q == '`') && s[len(s)-1] == q {
			s = strings.TrimSpace(s[1 : len(s)-1])
			continue
		}
		break
	}
	return s
}

// installCommand 认出 npx / pnpm dlx / bunx 的 skills add，返回 add 后面的参数。
func installCommand(in string) ([]string, bool) {
	words := shellWords(in)
	i := 0
	switch {
	case len(words) > 0 && words[0] == "npx":
		i = 1
		for i < len(words) && strings.HasPrefix(words[i], "-") { // npx -y skills add
			i++
		}
	case len(words) > 1 && words[0] == "pnpm" && words[1] == "dlx", len(words) > 1 && words[0] == "yarn" && words[1] == "dlx":
		i = 2
	case len(words) > 0 && (words[0] == "bunx" || words[0] == "pnpx"):
		i = 1
	default:
		return nil, false
	}
	if i+1 >= len(words) || !isSkillsPackage(words[i]) || words[i+1] != "add" && words[i+1] != "a" && words[i+1] != "install" && words[i+1] != "i" {
		return nil, false
	}
	return words[i+2:], true
}

func isSkillsPackage(w string) bool {
	return w == "skills" || strings.HasPrefix(w, "skills@")
}

// valueFlags 是 npx skills add 里带值的参数：值跟着一起忽略。
var valueFlags = []string{"-a", "--agent", "--agents"}

// commandArgs 从 add 后面的参数里取来源和 --skill；其余参数忽略。
func commandArgs(args []string) (src string, skills []string, ignored bool, err error) {
	for i := 0; i < len(args); i++ {
		w := args[i]
		name, val, hasVal := strings.Cut(w, "=")
		switch {
		case name == "--skill" || name == "-s":
			if hasVal {
				skills = append(skills, splitList(val)...)
				continue
			}
			// 可跟多个值，直到下一个参数；还没遇到来源时，带 / 的是来源
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && (src != "" || !looksLikeSource(args[i+1])) {
				i++
				skills = append(skills, splitList(args[i])...)
			}
		case slices.Contains(valueFlags, name):
			ignored = true
			for !hasVal && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && (src != "" || !looksLikeSource(args[i+1])) {
				i++
			}
		case strings.HasPrefix(w, "-"):
			ignored = true
		case src == "":
			src = w
		default:
			// 多余的位置参数（npx 也只认第一个来源）
			ignored = true
		}
	}
	if src == "" {
		return "", nil, false, ErrImportUnknown
	}
	return src, slices.DeleteFunc(skills, func(s string) bool { return s == "*" }), ignored, nil
}

// looksLikeSource 粗判一个参数是不是来源：技能名和 Agent 名都不带 /。
func looksLikeSource(w string) bool { return strings.Contains(w, "/") }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func appendSkills(dst []string, more ...string) []string {
	for _, s := range more {
		if s != "" && !slices.Contains(dst, s) {
			dst = append(dst, s)
		}
	}
	return dst
}

// shellWords 按空白切开，认单双引号和反斜杠转义（够用就行，不是完整的 shell）。
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	in, quote, esc := false, byte(0), false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			cur.WriteByte(c)
			esc, in = false, true
		case c == '\\' && quote != '\'':
			if i+1 < len(s) && s[i+1] == '\n' { // 续行
				i++
				continue
			}
			esc = true
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

func isLocalPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") || s == "~" ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "file://")
}

func localPath(s string) string {
	if p, ok := strings.CutPrefix(s, "file://"); ok {
		if u, err := url.PathUnescape(p); err == nil {
			p = u
		}
		return filepath.Clean(p)
	}
	return filepath.Clean(expandTilde(s))
}

// expandTilde 把开头的 ~ 换成主目录。
func expandTilde(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok || rest != "" && rest[0] != '/' {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, rest)
	}
	return p
}

// webURL 认出带协议的网址，以及不带协议的 github.com/…、skills.sh/… 和 clawhub.ai/…。
func webURL(in string) (*url.URL, bool) {
	raw := in
	if !strings.Contains(raw, "://") {
		host, _, _ := strings.Cut(raw, "/")
		h := strings.ToLower(host)
		if h != "github.com" && h != "www.github.com" &&
			h != "skills.sh" && h != "www.skills.sh" &&
			h != "clawhub.ai" && h != "www.clawhub.ai" &&
			h != "gitlab.com" {
			return nil, false
		}
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, false
	}
	return u, true
}

func parseWebURL(out ImportSource, u *url.URL) (ImportSource, error) {
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	segs := pathSegments(u.Path)
	switch host {
	case "github.com":
		if len(segs) < 2 {
			return out, ErrImportUnknown
		}
		repo := strings.TrimSuffix(segs[1], ".git")
		if !ghOwner.MatchString(segs[0]) || !ghRepo.MatchString(repo) {
			return out, ErrImportUnknown
		}
		out.Kind, out.Repo = ImportGitHub, segs[0]+"/"+repo
		// /tree/<ref>/<子目录>；/blob/<ref>/…/SKILL.md 取它所在的目录
		if len(segs) >= 4 && (segs[2] == "tree" || segs[2] == "blob") {
			out.Ref = segs[3]
			sub := strings.Join(segs[4:], "/")
			if segs[2] == "blob" && strings.EqualFold(path.Base(sub), "SKILL.md") {
				sub = path.Dir(sub)
				if sub == "." {
					sub = ""
				}
			}
			out.Subpath = sub
		}
		if f := u.Fragment; f != "" {
			ref, skill, _ := strings.Cut(f, "@")
			if out.Ref == "" {
				out.Ref = ref
			}
			out.Skills = appendSkills(out.Skills, skill)
		}
		return out, nil
	case "skills.sh":
		return parseSkillsShSegments(out, segs)
	case "clawhub.ai":
		return parseClawHubURL(out, segs)
	case "gitlab.com":
		out.Kind = ImportUnsupported
		return out, nil
	}
	out.Kind = ImportWebsite
	u.Fragment, u.RawQuery = "", ""
	// 直接给了某个技能的地址：…/.well-known/skills/<name>[/SKILL.md]
	if m := wellKnownSkillURL.FindStringSubmatch(u.Path); m != nil {
		u.Path, u.RawPath = m[1], ""
		if m[2] != "" && m[2] != "index.json" {
			out.Skills = appendSkills(out.Skills, m[2])
		}
	}
	out.URL = strings.TrimSuffix(u.String(), "/")
	return out, nil
}

func parseSkillsShSegments(out ImportSource, segs []string) (ImportSource, error) {
	// skills.sh/<owner>/<repo>/<skill>；owner 带点的是网站来源的技能
	if len(segs) < 2 {
		return out, ErrImportUnknown
	}
	if strings.Contains(segs[0], ".") {
		out.Kind, out.URL, out.Guessed = ImportWebsite, "https://"+segs[0], true
		out.Skills = []string{segs[len(segs)-1]}
		return out, nil
	}
	if !ghOwner.MatchString(segs[0]) || !ghRepo.MatchString(segs[1]) {
		return out, ErrImportUnknown
	}
	out.Kind, out.Repo = ImportGitHub, segs[0]+"/"+segs[1]
	if len(segs) >= 3 {
		out.Skills = []string{segs[2]}
	}
	return out, nil
}

func parseClawHubURL(out ImportSource, segs []string) (ImportSource, error) {
	var owner, slug string
	switch {
	case len(segs) == 4 && segs[0] == "skills-sh":
		return parseSkillsShSegments(out, segs[1:])
	case len(segs) == 3 && segs[1] == "skills":
		owner, slug = segs[0], segs[2]
	case len(segs) == 2 && !slices.Contains([]string{"skills", "user", "api"}, strings.ToLower(segs[0])):
		owner, slug = segs[0], segs[1]
	default:
		return out, ErrImportUnknown
	}
	if !validClawHubIdent(owner) || !validClawHubIdent(slug) {
		return out, ErrImportUnknown
	}
	out.Kind = ImportClawHub
	out.Repo = owner + "/" + slug
	out.URL = "https://clawhub.ai/" + owner + "/skills/" + slug
	return out, nil
}

func validClawHubIdent(s string) bool {
	return clawHubIdent.MatchString(s) && !strings.Contains(s, "..")
}

// parseShorthand 是不带协议的写法：owner/repo、owner/repo/子目录、
// owner/repo@skill、owner/repo#ref，以及带点的短名字（网站）。
func parseShorthand(out ImportSource, in string) (ImportSource, error) {
	body, frag, hasFrag := strings.Cut(in, "#")
	segs := pathSegments(body)
	if len(segs) < 2 {
		return out, ErrImportUnknown
	}
	if strings.Contains(segs[0], ".") {
		// mintlify.com/mintlify：先试域名根目录
		out.Kind, out.URL, out.Guessed = ImportWebsite, "https://"+segs[0], true
		out.Skills = []string{strings.TrimPrefix(segs[len(segs)-1], "@")}
		out.Subpath = strings.Join(segs[1:], "/") // 根目录找不到时再试 https://mintlify.com/docs 这样的地址
		return out, nil
	}
	last := segs[len(segs)-1]
	if name, skill, ok := strings.Cut(last, "@"); ok && len(segs) == 2 {
		segs[1] = name
		out.Skills = appendSkills(out.Skills, skill)
	}
	if !ghOwner.MatchString(segs[0]) || !ghRepo.MatchString(segs[1]) {
		return out, ErrImportUnknown
	}
	out.Kind, out.Repo = ImportGitHub, segs[0]+"/"+strings.TrimSuffix(segs[1], ".git")
	out.Subpath = strings.Join(segs[2:], "/")
	if hasFrag {
		ref, skill, _ := strings.Cut(frag, "@")
		out.Ref = ref
		out.Skills = appendSkills(out.Skills, skill)
	}
	return out, nil
}

func pathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			if u, err := url.PathUnescape(s); err == nil {
				s = u
			}
			out = append(out, s)
		}
	}
	return out
}

// SkillSlug 和 npx skills 的 toSkillSlug 一样：小写、空白和下划线换成
// 连字符、去掉其他符号。按 --skill 预选和请求 skills.sh 快照都用它。
func SkillSlug(name string) string {
	s := slugSpace.ReplaceAllString(strings.ToLower(name), "-")
	s = slugOther.ReplaceAllString(s, "")
	return strings.Trim(slugDashes.ReplaceAllString(s, "-"), "-")
}

var (
	slugSpace  = regexp.MustCompile(`[\s_]+`)
	slugOther  = regexp.MustCompile(`[^a-z0-9-]`)
	slugDashes = regexp.MustCompile(`-+`)
)
