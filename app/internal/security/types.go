// Package security 是本地 Skill 安全检查的分析核心：只分析调用方交来的
// 文件快照，不读磁盘、不联网、不运行任何进程。读文件、调度、存报告由
// internal/library 负责。
package security

import (
	"io/fs"
	"time"
)

// SchemaVersion 是报告结构的版本；EngineVersion 是分析流程的版本。
// 两者任一变化，旧报告都不能当作当前结果。
const (
	SchemaVersion = 1
	EngineVersion = 1
)

// Notice 是界面上固定的三种提示类型。
type Notice string

const (
	Info     Notice = "info"
	Warning  Notice = "warning"
	HighRisk Notice = "high_risk"
)

// Rank 给提示类型排优先级，越大越严重；未知类型为 0。
func (n Notice) Rank() int {
	switch n {
	case HighRisk:
		return 3
	case Warning:
		return 2
	case Info:
		return 1
	}
	return 0
}

// Risky 报告这一类型是否计入“有风险”：只有警告和高风险算。
func (n Notice) Risky() bool { return n == Warning || n == HighRisk }

// Confidence 表达证据是否充分，与提示类型分开。
type Confidence string

const (
	Low    Confidence = "low"
	Medium Confidence = "medium"
	High   Confidence = "high"
)

func (c Confidence) rank() int {
	switch c {
	case High:
		return 3
	case Medium:
		return 2
	}
	return 1
}

// Category 是问题所属的方向，不决定提示类型。
type Category string

const (
	RemoteExecution        Category = "remote_execution"
	CredentialHandling     Category = "credential_handling"
	DataExfiltration       Category = "data_exfiltration"
	InstructionOverride    Category = "instruction_override"
	Obfuscation            Category = "obfuscation"
	DestructivePersistence Category = "destructive_persistence"
	EmbeddedSecret         Category = "embedded_secret"
	FileBoundary           Category = "file_boundary"
	Capability             Category = "capability"
)

// Context 是命中位置的语境，详情据此说明“可能是示例”。
type Context string

const (
	CtxInstruction Context = "instruction" // SKILL.md 等文档里给 Agent 的指令或要执行的代码块
	CtxExample     Context = "example"     // 文档里标明是示例的代码
	CtxTeaching    Context = "teaching"    // 安全说明、反例里的引用
	CtxScript      Context = "script"      // 附带的脚本
	CtxConfig      Context = "config"      // 配置文件
	CtxStructure   Context = "structure"   // 目录结构本身
)

// FileKind 是快照里一个条目的类型。
type FileKind int

const (
	Regular FileKind = iota
	Symlink
	Special // 管道、设备、套接字等，不打开
)

// Gap 是覆盖缺口的原因代码。
type Gap string

const (
	GapSourcePartial   Gap = "source_partial"
	GapUnreadable      Gap = "unreadable"
	GapBinary          Gap = "unsupported_binary"
	GapEncoding        Gap = "unsupported_encoding"
	GapFileLimit       Gap = "file_limit"
	GapByteLimit       Gap = "byte_limit"
	GapDepthLimit      Gap = "depth_limit"
	GapDeadline        Gap = "deadline"
	GapChanged         Gap = "changed_during_scan"
	GapLinkNotFollowed Gap = "link_not_followed"
	GapSpecialFile     Gap = "special_file"
	GapAnalysisError   Gap = "analysis_error"
	GapFindingLimit    Gap = "finding_limit"
	GapCancelled       Gap = "cancelled"
)

// File 是快照里的一个条目。Path 是相对 Skill 根目录、用 / 分隔的路径。
type File struct {
	Path   string
	Kind   FileKind
	Mode   fs.FileMode // 权限位（含执行位）
	Data   []byte      // 普通文件读到的全部字节；未读取时为 nil
	Link   string      // 链接的原始目标
	Escape bool        // 链接能确定指向根目录外
	Gap    Gap         // 非空表示这一项没有读取或没有读全
}

// Input 是一次分析的输入。
type Input struct {
	Files      []File
	Incomplete bool   // 来源尚未补齐（只拿到了 SKILL.md 预览等）
	Gaps       []Hole // 读取阶段发现、和具体文件无关或已经记下的缺口
}

// Hole 是一处没有检查到的范围。Path 为空表示不针对某个文件。
type Hole struct {
	Path   string `json:"path,omitempty"`
	Reason Gap    `json:"reason"`
}

// Fingerprinter 为发现生成稳定标识。library 用本机密钥做 HMAC，
// 核心不接触密钥。
type Fingerprinter func(parts ...string) string

// Evidence 是一处证据位置。Snippet 已经脱敏。
type Evidence struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"` // 1 起；结构问题可为 0
	Snippet string `json:"snippet,omitempty"`
	Ref     bool   `json:"ref,omitempty"` // 引用位置（如 SKILL.md 里提到这个脚本），不是行为本身
}

// Finding 是一条发现。
type Finding struct {
	Key         string     `json:"key"` // 证据指纹，已看过标记靠它匹配
	Rule        string     `json:"rule"`
	RuleVersion int        `json:"ruleVersion"`
	Category    Category   `json:"category"`
	Notice      Notice     `json:"notice"`
	Confidence  Confidence `json:"confidence"`
	Context     Context    `json:"context"`
	Message     string     `json:"message"` // 文案键，界面映射到 locales
	Evidence    []Evidence `json:"evidence"`
}

// Execution 是一次分析的执行结果。
type Execution string

const (
	Completed Execution = "completed"
	Failed    Execution = "failed"
	Cancelled Execution = "cancelled"
	TimedOut  Execution = "timed_out"
)

// Report 是一次分析的完整结果，只含相对路径。
type Report struct {
	Schema          int       `json:"schema"`
	Engine          int       `json:"engine"`
	Ruleset         string    `json:"ruleset"`
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	Execution       Execution `json:"execution"`
	Error           string    `json:"error,omitempty"` // 失败原因（不含证据内容）
	ContentDigest   string    `json:"contentDigest,omitempty"`
	Complete        bool      `json:"complete"`
	Holes           []Hole    `json:"holes,omitempty"`
	External        []string  `json:"external,omitempty"` // 要从网上取来执行、没有检查到的远程内容（脱敏后的地址）
	FilesDiscovered int       `json:"filesDiscovered"`
	FilesAnalyzed   int       `json:"filesAnalyzed"`
	BytesRead       int64     `json:"bytesRead"`
	Findings        []Finding `json:"findings"`
}

// Top 是报告里最严重的提示类型；没有发现时为空。
func (r *Report) Top() Notice {
	var top Notice
	for _, f := range r.Findings {
		if f.Notice.Rank() > top.Rank() {
			top = f.Notice
		}
	}
	return top
}

// Count 数某一类型的发现。
func (r *Report) Count(n Notice) int {
	c := 0
	for _, f := range r.Findings {
		if f.Notice == n {
			c++
		}
	}
	return c
}

// Policy 是应用控制的扫描策略。待扫描目录里的任何配置都不能改它。
type Policy struct {
	MaxFindings int // 单个 Skill 最多保留的发现
}

// DefaultPolicy 是应用默认的策略。
func DefaultPolicy() Policy { return Policy{MaxFindings: 1000} }
