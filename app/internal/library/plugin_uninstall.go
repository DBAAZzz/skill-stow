package library

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"skillmanager/locales"
)

// pluginInstall 是 installed_plugins.json 里登记的一次安装，卸载时按它拼命令。
type pluginInstall struct {
	id      string // 注册名 plugin@market
	scope   string // user、project 或 local
	project string // project、local 范围装在哪个项目
}

const (
	claudeCode = "claude-code"
	workBuddy  = "workbuddy"
)

// CanUninstallPlugin 报告 s 所在的插件能不能在这里卸载：用户给 Claude Code、
// WorkBuddy 或 Codex 装的插件可以，靠它们自己的命令行卸载。内置的不行，Agent
// 会再装回来。
func (l *Library) CanUninstallPlugin(s *Skill) bool {
	return s != nil && s.Source == ViaPlugin && slices.Contains([]string{claudeCode, workBuddy, codex}, s.Provider) &&
		(s.install != nil || !l.disk)
}

// PluginSkills 是和 s 来自同一个插件的技能（含 s），卸载插件时一起没了。
func (l *Library) PluginSkills(s *Skill) []*Skill {
	if s == nil || s.Source != ViaPlugin {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(l.Skills), func(k *Skill) bool {
		return !samePlugin(k, s)
	})
}

func samePlugin(a, b *Skill) bool {
	if a.Source != ViaPlugin || a.Provider != b.Provider || a.Plugin != b.Plugin {
		return false
	}
	return a.install == nil || b.install == nil || a.install.id == b.install.id
}

// UninstallCommand 是卸载 s 所在插件时执行的命令，显示在确认框里。WorkBuddy
// 用的是它自带的 codebuddy 命令行。
func (l *Library) UninstallCommand(s *Skill) string {
	if s == nil {
		return ""
	}
	name := "claude"
	switch s.Provider {
	case workBuddy:
		name = "codebuddy"
	case codex:
		name = "codex"
	}
	return strings.Join(slices.Concat([]string{name}, uninstallArgs(s)), " ")
}

func uninstallArgs(s *Skill) []string {
	if s.Provider == codex {
		id := s.Plugin
		if s.install != nil {
			id = s.install.id
		}
		return []string{"plugin", "remove", id}
	}
	id, scope := s.Plugin, "user"
	if s.install != nil {
		id = s.install.id
		if s.install.scope != "" {
			scope = s.install.scope
		}
	}
	return []string{"plugin", "uninstall", id, "--scope", scope}
}

// pluginCmd 是一次卸载要执行的命令。
type pluginCmd struct {
	bin  string
	args []string
	env  []string // 加在当前环境变量后面
	dir  string
}

// UninstallPlugin 用 Agent 自己的命令行卸载 s 所在的整个插件，最多等一分钟。
// 不改内存里的库，可以在后台调用；卸完由调用方重新扫描或 ForgetPlugin。
// 示例数据不执行命令。
func (l *Library) UninstallPlugin(s *Skill) (err error) {
	if !l.CanUninstallPlugin(s) {
		return errors.New(locales.PluginUninstallUnsupported())
	}
	defer l.track("plugin", "uninstall", s.Plugin)(&err)
	if !l.disk {
		return nil
	}
	dir := l.Home
	if s.install.project != "" {
		dir = s.install.project
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch s.Provider {
	case workBuddy:
		return l.uninstallWorkBuddyPlugin(ctx, s, dir)
	case codex:
		return l.uninstallCodexPlugin(ctx, s)
	}
	bin := findClaudeBinary(l.Home)
	if bin == "" {
		return errors.New(locales.PluginUninstallNoCli())
	}
	stdout, stderr, err := runPluginCommand(ctx, pluginCmd{bin: bin, args: append(uninstallArgs(s), "--json"), dir: dir})
	if ctx.Err() != nil {
		return errors.New(locales.PluginUninstallTimeout())
	}
	return uninstallResult(stdout, stderr, err)
}

// runPluginCommand 可以在测试里换掉，免得真的调到 Agent 的命令行。
var runPluginCommand = func(ctx context.Context, c pluginCmd) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, c.bin, c.args...)
	cmd.Dir = c.dir
	cmd.Env = append(os.Environ(), c.env...)
	cmd.WaitDelay = time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// uninstallResult 读 claude 的 --json 打出的那一行；读不出或说失败了却没写
// 原因，就用标准错误和退出码。
func uninstallResult(stdout, stderr []byte, runErr error) error {
	var res struct {
		Outcome string `json:"outcome"`
		Message string `json:"message"`
	}
	for _, line := range bytes.Split(bytes.TrimSpace(stdout), []byte("\n")) {
		if json.Unmarshal(line, &res) == nil && res.Outcome != "" {
			break
		}
	}
	switch {
	case res.Outcome != "" && res.Outcome != "failed" && runErr == nil:
		return nil
	case res.Message != "":
		return errors.New(res.Message)
	case runErr == nil && res.Outcome != "failed":
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return errors.New(msg)
	}
	if runErr != nil {
		return runErr
	}
	return errors.New(locales.PluginUninstallFailedNoReason())
}

// findClaudeBinary 找 claude 命令。从访达打开的应用拿不到终端的 PATH，
// 所以 PATH 里没有时再看官方安装脚本和 Homebrew、npm 常放的位置。
func findClaudeBinary(home string) string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	for _, p := range []string{
		filepath.Join(home, ".local/bin/claude"),
		filepath.Join(home, ".claude/local/claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	} {
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

// uninstallCodexPlugin 用 codex plugin remove 卸载插件。它卸一个没装的插件也
// 照样成功退出，所以跑完重读配置和缓存，插件还在就算失败。
func (l *Library) uninstallCodexPlugin(ctx context.Context, s *Skill) error {
	bin := findCodexBinary(l.Home)
	if bin == "" {
		return errors.New(locales.PluginUninstallNoCodex())
	}
	ag, _ := l.agentByID(codex)
	home := filepath.Join(l.Home, ag.Home)
	_, stderr, err := runPluginCommand(ctx, pluginCmd{bin: bin, args: append(uninstallArgs(s), "--json"), dir: l.Home})
	if ctx.Err() != nil {
		return errors.New(locales.PluginUninstallTimeout())
	}
	still, readErr := codexPluginInstalled(home, s.install.id)
	if readErr == nil && !still {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return errors.New(msg)
	}
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	return errors.New(locales.PluginUninstallStillInstalled())
}

// codexPluginInstalled 报告 Codex 还登记着插件 id：config.toml 里有它的表
// （停用了也算），或者缓存里还有远程安装的标记。文件不存在算没登记；读不了、
// 读不懂时返回错误，这时不知道卸没卸掉。
func codexPluginInstalled(home, id string) (bool, error) {
	src, err := os.ReadFile(filepath.Join(home, "config.toml"))
	switch {
	case err == nil:
		plugins, _, err := parseCodexPlugins(string(src))
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(plugins, func(p codexPlugin) bool { return p.id == id }) {
			return true, nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	name, market, ok := strings.Cut(id, "@")
	if !ok || name == "" || market == "" {
		return false, nil
	}
	_, err = os.Stat(filepath.Join(home, codexPluginCache, market, name, codexRemoteMarker))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// codexCandidates 是 codex 命令可能在的位置，测试里换成假的。从访达打开的应用
// 拿不到终端的 PATH，所以还要看 Homebrew、nvm 和 Codex 应用里的。
var codexCandidates = func(home string) []string {
	out := []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex", filepath.Join(home, ".local/bin/codex")}
	if p, err := exec.LookPath("codex"); err == nil {
		out = append([]string{p}, out...)
	}
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm/versions/node/*/bin/codex"))
	// 先试 Node 版本号大的：按数字比，v22 排在 v9 前面
	slices.SortStableFunc(nvm, func(a, b string) int {
		return compareNodeVersions(nodeVersionOf(b), nodeVersionOf(a))
	})
	out = append(out, nvm...)
	for _, app := range []string{"/Applications/Codex.app", filepath.Join(home, "Applications/Codex.app")} {
		out = append(out, filepath.Join(app, "Contents/Resources/codex"))
	}
	return out
}

// findCodexBinary 找能直接运行的 codex。npm 装的 codex 是要 node 才能跑的
// 脚本，应用里不一定有 node，所以换成同一个包里的原生程序。
func findCodexBinary(home string) string {
	for _, c := range codexCandidates(home) {
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if strings.HasSuffix(real, ".js") {
			real = npmCodexNative(filepath.Dir(filepath.Dir(real)))
		}
		if real != "" && isExecutable(real) {
			return real
		}
	}
	return ""
}

// nodeVersionOf 是 nvm 路径 …/versions/node/v22.1.0/bin/codex 里的版本目录名。
func nodeVersionOf(codexPath string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(codexPath)))
}

// compareNodeVersions 按数字逐段比 v22.1.0 这样的版本号；读不出数字的段算 0，
// 都一样时按原文比。
func compareNodeVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return strings.Compare(a, b)
}

// npmCodexNative 是 npm 包 @openai/codex（pkg 是包目录）里随平台装的原生程序。
func npmCodexNative(pkg string) string {
	return npmCodexNativeFor(pkg, runtime.GOOS, runtime.GOARCH)
}

// npmCodexNativeFor 在包里找 goos/goarch 能跑的原生程序。包里可能带好几个
// 平台的 vendor 目录：先挑目录名写明这个系统和架构的，再挑没写平台的，写着
// 别的系统或架构的不用。
func npmCodexNativeFor(pkg, goos, goarch string) string {
	var neutral string
	for _, pattern := range []string{"node_modules/@openai/codex-*/vendor/*/bin/codex", "vendor/*/bin/codex", "vendor/*/codex/codex"} {
		found, _ := filepath.Glob(filepath.Join(pkg, pattern))
		for _, f := range found {
			if !isExecutable(f) {
				continue
			}
			switch vendorPlatformMatch(vendorTarget(f), goos, goarch) {
			case platformMatch:
				return f
			case platformUnknown:
				if neutral == "" {
					neutral = f
				}
			}
		}
	}
	return neutral
}

// vendorTarget 是原生程序所在的 vendor 子目录名，如 aarch64-apple-darwin。
func vendorTarget(f string) string {
	for dir := filepath.Dir(f); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(filepath.Dir(dir)) == "vendor" {
			return filepath.Base(dir)
		}
	}
	return ""
}

type platformFit int

const (
	platformUnknown platformFit = iota // 目录名没写系统和架构
	platformMatch                      // 写的就是这个系统和架构
	platformForeign                    // 写的是别的系统或架构
)

// 目录名里表示系统、架构的词，按 Go 的 GOOS、GOARCH 归类。
var (
	osWords   = map[string]string{"darwin": "darwin", "apple": "darwin", "macos": "darwin", "linux": "linux", "windows": "windows", "win32": "windows"}
	archWords = map[string]string{"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "x64": "amd64", "amd64": "amd64"}
)

// vendorPlatformMatch 看 vendor 目录名（rust 的 aarch64-apple-darwin 或 npm 的
// darwin-arm64 这样）是不是给 goos/goarch 用的。
func vendorPlatformMatch(target, goos, goarch string) platformFit {
	var gotOS, gotArch string
	for _, w := range strings.Split(strings.ToLower(target), "-") {
		if v, ok := osWords[w]; ok {
			if gotOS != "" && gotOS != v {
				return platformForeign
			}
			gotOS = v
		}
		if v, ok := archWords[w]; ok {
			if gotArch != "" && gotArch != v {
				return platformForeign
			}
			gotArch = v
		}
	}
	switch {
	case gotOS != "" && gotOS != goos, gotArch != "" && gotArch != goarch:
		return platformForeign
	case gotOS == goos && gotArch == goarch:
		return platformMatch
	}
	return platformUnknown
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// uninstallWorkBuddyPlugin 用 WorkBuddy 安装包里的 codebuddy 命令行卸载插件。
// 它不是单独装的命令：要像 WorkBuddy 那样用应用里的 Electron 当 node 跑，
// 并把配置目录指到 ~/.workbuddy，不然它改的是 CodeBuddy 的 ~/.codebuddy。
// 它卸载失败时退出码也是 0，所以跑完重读登记文件，插件还在就算失败。
func (l *Library) uninstallWorkBuddyPlugin(ctx context.Context, s *Skill, dir string) error {
	electron, cli := findWorkBuddyCli(l.Home)
	if cli == "" {
		return errors.New(locales.PluginUninstallNoWorkBuddy())
	}
	ag, _ := l.agentByID(workBuddy)
	config := filepath.Join(l.Home, ag.Home)
	stdout, stderr, err := runPluginCommand(ctx, pluginCmd{
		bin:  electron,
		args: append([]string{cli}, uninstallArgs(s)...),
		dir:  dir,
		env: []string{
			"ELECTRON_RUN_AS_NODE=1",
			"CODEBUDDY_CONFIG_DIR=" + config,
			"WORKBUDDY_CONFIG_DIR=" + config,
			"WORKBUDDY_DATA_FOLDER_NAME=" + ag.Home,
			"CODEBUDDY_PROJECT_CONFIG_DIR=" + ag.Home,
			"CODEBUDDY_FORCE_LITE_WB_BUNDLE=1",
			"CODEBUDDY_FORCE_HEADLESS_BUNDLE=1",
			"CODEBUDDY_HOST=workbuddy-desktop",
			"CODEBUDDY_PLUGIN_BACKGROUND_MANAGEMENT=0",
			"CODEBUDDY_DISABLE_PRODUCT_CACHE=1",
			"DISABLE_AUTOUPDATER=1",
		},
	})
	if ctx.Err() != nil {
		return errors.New(locales.PluginUninstallTimeout())
	}
	still, readErr := pluginRegistered(filepath.Join(l.Home, ag.Plugins, "installed_plugins.json"), s.install)
	if readErr == nil && !still {
		return nil
	}
	if msg := workBuddyFailure(stdout); msg != "" {
		return errors.New(msg)
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return errors.New(msg)
	}
	if err != nil {
		return err
	}
	return errors.New(locales.PluginUninstallStillInstalled())
}

// workBuddyApps 是 WorkBuddy 应用可能在的位置，测试里换成假的。
var workBuddyApps = func(home string) []string {
	return []string{"/Applications/WorkBuddy.app", filepath.Join(home, "Applications/WorkBuddy.app")}
}

// findWorkBuddyCli 找 WorkBuddy 应用里的 Electron 和 codebuddy 命令行。
func findWorkBuddyCli(home string) (electron, cli string) {
	for _, app := range workBuddyApps(home) {
		e := filepath.Join(app, "Contents/MacOS/Electron")
		c := filepath.Join(app, "Contents/Resources/app.asar.unpacked/cli/bin/codebuddy")
		if _, err := os.Stat(c); err == nil && isExecutable(e) {
			return e, c
		}
	}
	return "", ""
}

// pluginRegistered 报告 installed_plugins.json 里还有没有 inst 这次安装。
func pluginRegistered(file string, inst *pluginInstall) (bool, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	type entry struct {
		Scope       string `json:"scope"`
		ProjectPath string `json:"projectPath"`
	}
	var reg struct {
		Plugins map[string][]entry `json:"plugins"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		return false, err
	}
	return slices.ContainsFunc(reg.Plugins[inst.id], func(e entry) bool {
		return e.Scope == inst.scope && e.ProjectPath == inst.project
	}), nil
}

// workBuddyFailure 是 codebuddy 命令行打出的失败原因（「✘ Failed to uninstall
// plugin(s): 原因」那一行里的原因）。
func workBuddyFailure(stdout []byte) string {
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "✘"); ok {
			rest = strings.TrimSpace(rest)
			if _, reason, ok := strings.Cut(rest, ": "); ok {
				return strings.TrimSpace(reason)
			}
			return rest
		}
	}
	return ""
}

// ForgetPlugin 把 s 所在插件的技能从内存里的库拿掉，卸载成功后、下次扫描前
// 界面不再显示它们。示例数据没有扫描，靠它体现卸载。
func (l *Library) ForgetPlugin(s *Skill) {
	for _, k := range l.PluginSkills(s) {
		l.unlist(k)
	}
}
