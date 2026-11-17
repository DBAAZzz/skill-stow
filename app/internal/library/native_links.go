package library

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"skillmanager/locales"
)

func (l *Library) CanAddToAgent(s *Skill, id string) bool {
	if s.ReadOnly() || s.DiscoveredIn(id) || !slices.ContainsFunc(l.Agents, func(a Agent) bool { return a.ID == id }) {
		return false
	}
	a, ok := l.agentByID(id)
	if !ok {
		return false
	}
	if !l.disk {
		return true
	}
	_, err := os.Lstat(filepath.Join(l.Home, a.Skills, s.Name))
	return errors.Is(err, os.ErrNotExist)
}
func (l *Library) AddToAgent(s *Skill, id string) (err error) {
	defer l.track("link", "add", s.Name+" @ "+id)(&err)
	if !l.CanAddToAgent(s, id) {
		return errors.New(locales.NativeLinkAlreadyAvailable())
	}
	if !l.disk {
		if s.Available == nil {
			s.Available = slices.Clone(s.Enabled)
		}
		s.Available = append(s.Available, id)
		s.Enabled = append(s.Enabled, id)
		if s.States == nil {
			s.States = map[string]AgentState{}
		}
		kind := StateEnabled
		if !nativeSupported(id) {
			kind = StateUnsupported
		}
		s.States[id] = AgentState{Discoverable: true, Enabled: true, Kind: kind}
		if l.nativeData.Links == nil {
			l.nativeData = emptyNativeData()
		}
		l.nativeData.Links[s.ID+id] = s.Real
		return nil
	}
	l.loadNativeData()
	if l.nativeErr != nil {
		return l.nativeErr
	}
	if err := l.link(s, id); err != nil {
		return err
	}
	l.resolve(s)
	ag, _ := l.agentByID(id)
	l.nativeData.Links[filepath.Join(l.Home, ag.Skills, s.Name)] = s.Real
	return l.saveNativeData()
}
func (l *Library) CanRemoveFromAgent(s *Skill, id string) bool {
	if !l.disk {
		_, ok := l.nativeData.Links[s.ID+id]
		return ok
	}
	p := s.Links[id]
	target, owned := l.nativeData.Links[p]
	if !owned {
		return false
	}
	if !isLink(p) {
		return false
	}
	real, err := realPath(p)
	return err == nil && real == target
}
func (l *Library) RemoveFromAgent(s *Skill, id string) (err error) {
	defer l.track("link", "remove", s.Name+" @ "+id)(&err)
	if !l.CanRemoveFromAgent(s, id) {
		return errors.New(locales.NativeRemoveAppLinksOnly())
	}
	if !l.disk {
		delete(l.nativeData.Links, s.ID+id)
		delete(s.States, id)
		s.Available = slices.DeleteFunc(s.Available, func(v string) bool { return v == id })
		s.Enabled = slices.DeleteFunc(s.Enabled, func(v string) bool { return v == id })
		return nil
	}
	path := s.Links[id]
	if err := l.unlink(s, id); err != nil {
		return err
	}
	delete(l.nativeData.Links, path)
	l.resolve(s)
	return l.saveNativeData()
}

// NameImpacts lists other physical skills affected by a name-based disable, including projects.
func (l *Library) NameImpacts(s *Skill, id string) []string {
	if pathKeyed(id) || !nativeSupported(id) {
		return nil
	}
	name, path := s.nativeTarget(id)
	match := func(n string) bool {
		if id == "gemini" {
			return strings.EqualFold(n, name)
		}
		return n == name
	}
	var out []string
	add := func(dir string) {
		p := canonicalSkillPath(filepath.Join(dir, "SKILL.md"))
		if p == path {
			return
		}
		n := readSkillMD(dir).Name
		if n == "" && id != "opencode" {
			n = filepath.Base(dir)
		}
		if match(n) && !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}
	if !l.disk {
		if s.Copies > 0 {
			return []string{locales.CopiesLinkCopy(s.Dir)}
		}
		return nil
	}
	for _, skill := range l.Skills {
		if skill.Source == Mine {
			for _, p := range skill.places {
				if slices.Contains(l.readers[p.dir], id) {
					add(p.real)
				}
			}
		} else if skill.Source == Project {
			for _, r := range skill.Reads {
				if r.Agent == id {
					add(skill.Real)
					break
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
