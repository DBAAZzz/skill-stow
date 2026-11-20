package library

import (
	"path/filepath"
	"slices"
	"strings"
)

type gitChange struct {
	path             string
	ignored, deleted bool
}

func gitRecords(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\x00"), "\x00")
}

func parseGitStatus(data string) ([]gitChange, bool, error) {
	rows := gitRecords(data)
	var changes []gitChange
	var oid string
	for i := 0; i < len(rows); i++ {
		row := rows[i]
		tag, rest, ok := strings.Cut(row, " ")
		if !ok {
			return nil, false, errGitOutput
		}
		switch tag {
		case "#":
			if v, ok := strings.CutPrefix(rest, "branch.oid "); ok {
				oid = v
			}
		case "?", "!":
			changes = append(changes, gitChange{path: strings.TrimSuffix(rest, "/"), ignored: tag == "!"})
		case "1", "2", "u":
			n := map[string]int{"1": 8, "2": 9, "u": 10}[tag]
			fields := strings.SplitN(rest, " ", n)
			if len(fields) != n {
				return nil, false, errGitOutput
			}
			xy := fields[0]
			// 冲突的 XY 可能含 D，但文件仍在工作区。mW 才表示磁盘上有没有。
			worktreeMode := fields[4]
			if tag == "u" {
				worktreeMode = fields[5]
			}
			changes = append(changes, gitChange{path: fields[n-1], deleted: worktreeMode == "000000"})
			if tag == "2" {
				i++
				if i == len(rows) {
					return nil, false, errGitOutput
				}
				if strings.Contains(xy, "R") {
					changes = append(changes, gitChange{path: rows[i], deleted: true})
				}
			}
		default:
			return nil, false, errGitOutput
		}
	}
	if oid == "" {
		return nil, false, errGitOutput
	}
	return changes, oid != "(initial)", nil
}

type gitDirFacts struct {
	tracked bool
	changes []gitChange
}

// status 可能只报被忽略的父目录，技能目录本身不会再有一条记录。
func (f *gitDirFacts) state(path string, dirty, hasIgnored bool, ignored map[string]bool) GitState {
	if f.tracked {
		if dirty {
			return GitModified
		}
		return gitClean
	}
	if dirty {
		return GitAdded
	}
	if hasIgnored {
		return gitIgnored
	}
	for path != "." {
		if ignored[path] {
			return gitIgnored
		}
		path = filepath.ToSlash(filepath.Dir(path))
	}
	return GitAdded
}

func (s *ProjectGit) collectInstalls(root string, p *ProjectInfo, changes []gitChange, head map[string]bool) {
	// 按目录归集一次，避免每个技能都遍历整份状态。
	byDir := map[string]*gitDirFacts{}
	for _, i := range p.gitScan.installs {
		byDir[gitRel(root, i.path)] = &gitDirFacts{}
		byDir[gitRel(root, i.real)] = &gitDirFacts{}
	}
	walk := func(path string, visit func(*gitDirFacts)) {
		for {
			if f := byDir[path]; f != nil {
				visit(f)
			}
			j := strings.LastIndexByte(path, '/')
			if j < 0 {
				break
			}
			path = path[:j]
		}
	}
	for path := range head {
		walk(path, func(f *gitDirFacts) { f.tracked = true })
	}
	ignored := map[string]bool{}
	for _, ch := range changes {
		if ch.ignored {
			ignored[ch.path] = true
		}
		walk(ch.path, func(f *gitDirFacts) { f.changes = append(f.changes, ch) })
	}
	for _, i := range p.gitScan.installs {
		var inst GitInstall
		paths := []string{i.path}
		if i.real != i.path {
			paths = append(paths, i.real)
		}
		for _, path := range paths {
			key := gitRel(root, path)
			facts := byDir[key]
			dirty, hasIgnored := false, false
			for _, ch := range facts.changes {
				if ch.ignored {
					hasIgnored = true
					continue
				}
				dirty = true
				if ch.deleted || ch.path == key {
					continue
				}
				state := GitAdded
				if head[ch.path] {
					state = GitModified
				}
				inst.Files = append(inst.Files, GitFile{strings.TrimPrefix(ch.path, key+"/"), state})
			}
			state := facts.state(key, dirty, hasIgnored, ignored)
			if gitPriority(state) > gitPriority(inst.State) {
				inst.State = state
			}
		}
		slices.SortFunc(inst.Files, func(a, b GitFile) int { return strings.Compare(a.Name, b.Name) })
		s.Installs[i.real] = append(s.Installs[i.real], inst)
	}
}

func gitPriority(state GitState) int {
	switch state {
	case GitAdded:
		return 3
	case GitModified:
		return 2
	case gitIgnored:
		return 1
	default:
		return 0
	}
}
