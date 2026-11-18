package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 本文件里的中文是检测模式（识别 Skill 文档里的中文指令），不是界面文案；
// 界面文案只通过 Finding.Message 的键在 locales 里取。

// rule 是一条自主编写的检测规则。判定语义变化时提升 version：该规则旧的
// “已看过”标记随之失效，其他规则不受影响。措辞修改只改 locales，不动版本。
type rule struct {
	id      string
	version int
	cat     Category
	base    Notice // 在指令或脚本语境里的类型；示例降一级，安全说明里的引用只算提示
	msg     string
	fixed   bool // 类型不随语境变化（能力提示、结构问题）
}

// 规则消息键：界面把它们映射到 locales 里的摘要与说明。
const (
	MsgRemotePipe        = "remote_pipe"
	MsgRemoteDownloadRun = "remote_download_run"
	MsgReverseShell      = "reverse_shell"
	MsgEncodedExec       = "encoded_exec"
	MsgBidi              = "bidi_chars"
	MsgInvisible         = "invisible_chars"
	MsgHiddenComment     = "hidden_comment"
	MsgIgnoreInstr       = "ignore_instructions"
	MsgConceal           = "conceal_from_user"
	MsgBypassConfirm     = "bypass_confirmation"
	MsgLeakContext       = "leak_context"
	MsgSecretInChat      = "secret_in_chat"
	MsgReadCredentials   = "read_credentials"
	MsgCredentialUpload  = "credential_upload"
	MsgSecretEnvUpload   = "secret_env_upload"
	MsgWipeHome          = "wipe_home"
	MsgPersistence       = "persistence"
	MsgEmbeddedSecret    = "embedded_secret"
	MsgLinkOutside       = "link_outside"
	MsgSpecialFile       = "special_file"
	MsgUsesNetwork       = "uses_network"
	MsgHasScripts        = "has_scripts"
	MsgHasBinary         = "has_binary"
	MsgNeedsAdmin        = "needs_admin"
)

var (
	rRemotePipe     = rule{"RE001", 1, RemoteExecution, Warning, MsgRemotePipe, false}
	rDownloadRun    = rule{"RE002", 1, RemoteExecution, Warning, MsgRemoteDownloadRun, false}
	rReverseShell   = rule{"RE004", 1, RemoteExecution, HighRisk, MsgReverseShell, false}
	rEncodedExec    = rule{"OB001", 1, Obfuscation, Warning, MsgEncodedExec, false}
	rBidi           = rule{"OB002", 1, Obfuscation, Warning, MsgBidi, true}
	rInvisible      = rule{"OB003", 1, Obfuscation, Warning, MsgInvisible, true}
	rHiddenComment  = rule{"OB004", 1, Obfuscation, Warning, MsgHiddenComment, false}
	rIgnoreInstr    = rule{"IO001", 1, InstructionOverride, Warning, MsgIgnoreInstr, false}
	rConceal        = rule{"IO002", 1, InstructionOverride, Warning, MsgConceal, false}
	rBypass         = rule{"IO003", 1, InstructionOverride, Warning, MsgBypassConfirm, false}
	rLeakContext    = rule{"IO004", 1, DataExfiltration, Warning, MsgLeakContext, false}
	rSecretInChat   = rule{"CR001", 1, CredentialHandling, Warning, MsgSecretInChat, false}
	rReadCreds      = rule{"CR002", 1, CredentialHandling, Warning, MsgReadCredentials, false}
	rCredUpload     = rule{"EX001", 1, DataExfiltration, HighRisk, MsgCredentialUpload, false}
	rSecretEnv      = rule{"EX002", 1, DataExfiltration, Warning, MsgSecretEnvUpload, false}
	rWipeHome       = rule{"DS001", 1, DestructivePersistence, HighRisk, MsgWipeHome, false}
	rPersistence    = rule{"DS002", 1, DestructivePersistence, Warning, MsgPersistence, false}
	rEmbeddedSecret = rule{"SE001", 1, EmbeddedSecret, Warning, MsgEmbeddedSecret, true}
	rLinkOutside    = rule{"FB001", 1, FileBoundary, Warning, MsgLinkOutside, true}
	rSpecialFile    = rule{"FB002", 1, FileBoundary, Warning, MsgSpecialFile, true}
	rUsesNetwork    = rule{"CP001", 1, Capability, Info, MsgUsesNetwork, true}
	rHasScripts     = rule{"CP002", 1, Capability, Info, MsgHasScripts, true}
	rHasBinary      = rule{"CP003", 1, Capability, Info, MsgHasBinary, true}
	rNeedsAdmin     = rule{"CP004", 1, Capability, Info, MsgNeedsAdmin, true}
)

var allRules = []rule{
	rRemotePipe, rDownloadRun, rReverseShell, rEncodedExec, rBidi, rInvisible, rHiddenComment,
	rIgnoreInstr, rConceal, rBypass, rLeakContext, rSecretInChat, rReadCreds, rCredUpload, rSecretEnv,
	rWipeHome, rPersistence, rEmbeddedSecret, rLinkOutside, rSpecialFile,
	rUsesNetwork, rHasScripts, rHasBinary, rNeedsAdmin,
}

// Messages 列出所有规则会用到的消息键，供界面测试核对每个键都有文案。
func Messages() []string {
	var out []string
	for _, r := range allRules {
		out = append(out, r.msg)
	}
	return out
}

// RulesetVersion 是全部规则 ID 与版本的摘要。规则增删或任何一条升级，
// 它都会变，旧报告随之需要重扫。
func RulesetVersion() string {
	ids := make([]string, 0, len(allRules))
	for _, r := range allRules {
		ids = append(ids, fmt.Sprintf("%s@%d", r.id, r.version))
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, ",")))
	return hex.EncodeToString(sum[:6])
}

// RuleVersion 返回规则当前的版本；未知规则返回 0。
func RuleVersion(id string) int {
	for _, r := range allRules {
		if r.id == id {
			return r.version
		}
	}
	return 0
}

// classify 按语境给出提示类型与置信度。高风险只在指令或脚本里成立。
func classify(r rule, ctx Context, conf Confidence) (Notice, Confidence) {
	if r.fixed {
		return r.base, conf
	}
	switch ctx {
	case CtxTeaching:
		return Info, Low
	case CtxExample:
		n := Info
		if r.base == HighRisk {
			n = Warning
		}
		if conf.rank() > Medium.rank() {
			conf = Medium
		}
		return n, conf
	}
	return r.base, conf
}

// ---- 敏感对象 ----

var (
	homePrefix = `(?:~|\$HOME|\$\{HOME\}|/Users/[^/\s"']+|/home/[^/\s"']+)`
	// 私钥、云凭据、认证配置、钥匙串等。普通的 ~/.ssh/config、known_hosts 不算。
	sensitivePath = regexp.MustCompile(homePrefix + `/(?:\.ssh/(?:id_[A-Za-z0-9_]+(?:\.pub)?|[^\s'"/]*_key\b|identity)|\.aws/credentials|\.netrc|\.git-credentials|\.config/gh/hosts\.yml|\.docker/config\.json|\.kube/config|\.gnupg/|Library/Keychains/|\.npmrc|\.pypirc|\.config/gcloud/)`)
	keychainRead  = regexp.MustCompile(`\bsecurity\s+(?:find-generic-password|find-internet-password|dump-keychain)\b`)
	secretEnv     = regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|APIKEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIALS?))\}?`)
	urlPattern    = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>)\]]+`)
)

// sensitiveIn 返回文本里第一个敏感路径；公钥（.pub）不算。
func sensitiveIn(s string) string {
	for _, m := range sensitivePath.FindAllString(s, -1) {
		if !strings.HasSuffix(m, ".pub") {
			return m
		}
	}
	if m := keychainRead.FindString(s); m != "" {
		return m
	}
	return ""
}

// ---- 内嵌凭据 ----

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY(?: BLOCK)?-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{60,}\b`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),
}

var placeholderWords = regexp.MustCompile(`(?i)x{4,}|your|example|dummy|fake|sample|placeholder|redacted|replace|changeme|\.\.\.|\*{3,}|<[^>]*>|0{8,}|1234567`)

// realSecret 判断命中的值是不是像真的：占位符、重复字符、示例写法都不算。
// 私钥看后面一行是不是真的 base64 内容。
func realSecret(match, next string) bool {
	if strings.HasPrefix(match, "-----BEGIN") {
		body := strings.TrimSpace(next)
		return len(body) >= 40 && !placeholderWords.MatchString(body) && !strings.Contains(body, " ")
	}
	if placeholderWords.MatchString(match) {
		return false
	}
	distinct := map[rune]bool{}
	for _, r := range match {
		distinct[r] = true
	}
	return len(distinct) >= 10
}

// ---- 文档里的指令 ----

var (
	ignoreInstr = regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override)\s+(?:all\s+|any\s+)?(?:of\s+)?(?:the\s+|your\s+)?(?:previous|prior|above|earlier|preceding|system|original)\s+(?:instructions?|prompts?|rules|messages|guidelines)|(?:忽略|无视|忘记|忘掉|覆盖)(?:你)?(?:之前|以上|前面|上面|先前|系统|原有)(?:的)?(?:所有|全部|一切)?(?:指令|指示|提示词?|说明|规则|要求)`)
	conceal     = regexp.MustCompile(`(?i)\b(?:do\s+not|don'?t|never)\s+(?:tell|inform|notify|alert|show|mention\s+(?:this\s+|it\s+)?to|reveal\s+(?:this\s+|it\s+)?to)\s+the\s+user\b|\bwithout\s+(?:telling|informing|notifying|alerting)\s+the\s+user\b|\bhide\s+(?:this|it|the\s+\w+)\s+from\s+the\s+user\b|(?:不要|别|切勿|不得)(?:告诉|通知|提醒|让|向)用户|瞒着用户|(?:悄悄|偷偷|暗中)(?:地)?(?:执行|运行|发送|上传|修改|删除|安装)`)
	bypass      = regexp.MustCompile(`(?i)--dangerously-skip-permissions|\bbypass(?:ing)?\s+(?:the\s+|any\s+|all\s+)?(?:permission|approval|confirmation|sandbox|safety)|\bwithout\s+(?:asking\s+(?:the\s+user\s+)?for\s+)?(?:confirmation|permission|approval)\b|(?:跳过|绕过)(?:用户)?(?:的)?(?:确认|授权|审批|批准|权限|沙箱)|(?:无需|不需要|不用|不必)(?:征求|询问|经过)?用户(?:的)?(?:确认|授权|同意|批准)`)
	leakContext = regexp.MustCompile(`(?i)\b(?:send|post|upload|forward|exfiltrate|transmit)\s+(?:the\s+|your\s+|all\s+|this\s+)?(?:conversation|chat\s+history|context|system\s+prompt|instructions|memory|session)\s+(?:\w+\s+)?to\b|(?:发送|上传|转发|提交|发)(?:当前|全部|所有|完整)?(?:的)?(?:对话|聊天记录|上下文|系统提示词?)(?:内容)?(?:到|给|至)`)
	secretChat  = regexp.MustCompile(`(?i)\bpaste\s+(?:your\s+|the\s+)?(?:api[\s_-]?key|token|password|secret|private\s+key|credentials?)\s+(?:here|into\s+(?:the\s+|this\s+)?(?:chat|conversation)|in\s+(?:the\s+|this\s+)?(?:chat|conversation))|\b(?:send|give|tell)\s+me\s+your\s+(?:api[\s_-]?key|token|password|secret|private\s+key)|(?:把|将)?(?:你的)?(?:API\s*Key|api\s*key|密钥|令牌|token|Token|密码|私钥)(?:直接)?(?:粘贴|贴|发送|发|告诉)(?:到|给|在)?(?:聊天|对话|这里|我)`)
	commentCmd  = regexp.MustCompile(`(?i)\b(?:run|execute|curl|wget|ignore|send|upload|delete|rm\s+-|you\s+must|always|never\s+tell)\b|执行|运行|忽略|发送|上传|删除|必须|不要告诉`)
)

// ---- 逐行模式：shell 解析不了的脚本、文档正文和其他语言的脚本用 ----

var (
	linePipe         = regexp.MustCompile(`(?i)\b(?:curl|wget|fetch|iwr|irm|invoke-webrequest|invoke-restmethod)\b[^|\n]*\|\s*(?:sudo\s+(?:-\S+\s+)*)?(?:(?:ba|z|da|k|fi)?sh|python[0-9.]*|perl|ruby|node|iex|invoke-expression)\b`)
	lineSubst        = regexp.MustCompile(`(?i)\b(?:ba|z|da)?sh\s+(?:-\S+\s+)*-c\s+["']?\$\(\s*(?:curl|wget)\b|(?:\bsource|\beval|^\s*\.)\s+["']?(?:<\(|\$\()\s*(?:curl|wget)\b|\b(?:ba|z)?sh\s+<\(\s*(?:curl|wget)\b`)
	lineScriptRemote = regexp.MustCompile(`(?i)\b(?:exec|eval)\s*\(\s*(?:requests\.get|urllib\.request\.urlopen|urlopen|(?:await\s+)?\(?\s*await\s+fetch|fetch)\s*\(`)
	lineEncoded      = regexp.MustCompile(`(?i)\bbase64\s+(?:-d|--decode|-D)\b[^|\n]*\|\s*(?:sudo\s+)?(?:(?:ba|z|da)?sh|python[0-9.]*|perl)\b|\b(?:exec|eval)\s*\(\s*(?:base64\.b64decode|atob|Buffer\.from)\s*\(|\beval\s+["']?\$\(\s*(?:echo|printf)\b[^)]*\|\s*base64\s+(?:-d|--decode|-D)`)
	lineReverse      = regexp.MustCompile(`(?i)/dev/(?:tcp|udp)/|\b(?:nc|ncat|netcat)\b[^\n]*\s-(?:e|c)\s|\bsocat\b[^\n]*\bexec:`)
	lineWipe         = regexp.MustCompile(`\brm\s+(?:-[a-zA-Z]*[rR][a-zA-Z]*\s+|--recursive\s+|--force\s+|-f\s+)+(?:--no-preserve-root\s+)?["']?(?:/|/\*|~|~/|~/\*|\$HOME|\$\{HOME\}|\$HOME/\*|\$\{HOME\}/\*)["']?(?:\s|$|;|&|\|)`)
	linePersist      = regexp.MustCompile(`(?:>>?|\btee\s+-a)\s*["']?(?:~|\$HOME|\$\{HOME\})/\.(?:bashrc|zshrc|bash_profile|profile|zprofile|zshenv|config/fish/config\.fish)\b|Library/Launch(?:Agents|Daemons)/|\blaunchctl\s+(?:load|bootstrap|enable)\b|\bcrontab\s+(?:-e\b|[^-\s|])`)
	lineNetwork      = regexp.MustCompile(`(?i)\b(?:curl|wget)\b|requests\.(?:get|post|put)\(|\bfetch\(|urllib\.request|http\.client|\baxios\b|XMLHttpRequest|invoke-webrequest|invoke-restmethod|\bscp\b|\brsync\b.*:`)
	lineUpload       = regexp.MustCompile(`(?i)\bcurl\b[^\n]*\s(?:-d|--data(?:-binary|-raw|-urlencode)?|-F|--form|-T|--upload-file|--json)\b|\bwget\b[^\n]*--post-(?:data|file)|requests\.(?:post|put)\(|\bscp\b|\bnc\b\s+\S+\s+\d+`)
	lineSudo         = regexp.MustCompile(`(?:^|[;&|]\s*|\s)sudo\s+\S`)
)

// ---- 脱敏 ----

var (
	urlSecretParam = regexp.MustCompile(`(?i)([?&](?:token|access_token|key|api_key|apikey|secret|password|sig|signature|auth)=)[^&\s"']+`)
	urlUserinfo    = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
	bearer         = regexp.MustCompile(`(?i)(bearer\s+|token\s+|authorization:\s*)[A-Za-z0-9._~+/=-]{12,}`)
)

const maxSnippet = 160

// redact 把证据片段里的密钥、URL 里的凭据替换掉，并截短。
func redact(s string) string {
	s = strings.TrimSpace(s)
	for _, re := range secretPatterns[1:] {
		s = re.ReplaceAllStringFunc(s, mask)
	}
	s = urlSecretParam.ReplaceAllString(s, "${1}••••")
	s = urlUserinfo.ReplaceAllString(s, "${1}••••@")
	s = bearer.ReplaceAllString(s, "${1}••••")
	if r := []rune(s); len(r) > maxSnippet {
		s = string(r[:maxSnippet-1]) + "…"
	}
	return s
}

func mask(v string) string {
	r := []rune(v)
	if len(r) <= 6 {
		return "••••"
	}
	return string(r[:4]) + "••••"
}

// redactURL 只保留地址的协议、主机和路径，去掉查询参数与用户信息。
func redactURL(u string) string {
	u = urlUserinfo.ReplaceAllString(u, "${1}")
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if r := []rune(u); len(r) > 120 {
		u = string(r[:119]) + "…"
	}
	return u
}
