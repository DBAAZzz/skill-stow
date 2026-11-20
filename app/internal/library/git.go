package library

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// GitCheck 区分尚未检查、检查完成和无法检查的项目。
type GitCheck uint8

const (
	GitPending GitCheck = iota
	GitReady
	GitUnchecked
)

// GitState 描述一个安装位置相对 HEAD 的变化。
type GitState uint8

const (
	gitClean GitState = iota
	GitAdded
	GitModified
	gitIgnored
)

// GitInstall 把链接入口和技能文件的状态合在同一个安装位置下。
type GitInstall struct {
	State GitState
	Files []GitFile
}

// GitFile 记录相对技能目录的路径，供菜单定位有改动的文件。
type GitFile struct {
	Name  string
	State GitState
}

// ProjectGit 保存一次检查的结果，按技能真实目录归集各个安装位置。
type ProjectGit struct {
	Check    GitCheck
	Installs map[string][]GitInstall
}

type gitPlaces struct {
	dir      string // 扫描目录的真实路径，与 Git 返回的仓库根使用同一前缀
	dirs     []string
	installs []gitPlace
}
type gitPlace struct{ path, real string }

var localGit = sync.OnceValue(probeGit)

func probeGit() string {
	path, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	return checkGitTools(path)
}

func checkGitTools(path string) string {
	if runtime.GOOS == "darwin" && path == "/usr/bin/git" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if exec.CommandContext(ctx, "xcode-select", "-p").Run() != nil {
			return ""
		}
	}
	return path
}

func runLocalGit(bin, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(os.Environ(), func(v string) bool {
		k, _, _ := strings.Cut(v, "=")
		return k == "GIT_DIR" || k == "GIT_WORK_TREE" || k == "GIT_INDEX_FILE"
	})
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1", "LC_ALL=C")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return string(out), err
}

var errGitOutput = errors.New("invalid git output")
var gitSlots = make(chan struct{}, 4)

// CheckGit 在后台读取扫描时发现的安装位置，在一次扫描之内可以重新调用。
func (p *ProjectInfo) CheckGit() *ProjectGit {
	bin := localGit()
	if bin == "" {
		return &ProjectGit{Check: GitUnchecked}
	}
	gitSlots <- struct{}{}
	defer func() { <-gitSlots }()
	return p.readGit(bin)
}

func (p *ProjectInfo) readGit(bin string) *ProjectGit {
	s, err := p.gitSnapshot(bin)
	if err != nil {
		return &ProjectGit{Check: GitUnchecked}
	}
	return s
}

func (p *ProjectInfo) gitSnapshot(bin string) (*ProjectGit, error) {
	out, err := runLocalGit(bin, p.Dir, "rev-parse", "--show-toplevel", "--show-superproject-working-tree")
	if err != nil {
		return nil, err
	}
	root, super, _ := strings.Cut(strings.TrimSuffix(out, "\n"), "\n")
	root = filepath.FromSlash(root)
	if super != "" {
		return &ProjectGit{Check: GitUnchecked}, nil
	}
	paths := make([]string, 0, len(p.gitScan.dirs))
	for _, dir := range p.gitScan.dirs {
		paths = append(paths, gitRel(root, filepath.Join(p.gitScan.dir, dir)))
	}
	for _, i := range p.gitScan.installs {
		if i.real != i.path && isUnderDir(i.real, root) {
			paths = append(paths, gitRel(root, i.real))
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	status, err := runLocalGit(bin, root, append([]string{"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=all", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	changes, hasHEAD, err := parseGitStatus(status)
	if err != nil {
		return nil, err
	}
	head := map[string]bool{}
	if hasHEAD {
		tree, err := runLocalGit(bin, root, append([]string{"ls-tree", "-rz", "--full-tree", "--name-only", "HEAD", "--"}, paths...)...)
		if err != nil {
			return nil, err
		}
		for _, name := range gitRecords(tree) {
			head[name] = true
		}
	}
	s := &ProjectGit{Check: GitReady, Installs: map[string][]GitInstall{}}
	s.collectInstalls(root, p, changes, head)
	return s, nil
}

func gitRel(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return filepath.ToSlash(rel)
}
