package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"skillmanager/locales"
)

// configParseError 保留给界面显示的原生配置解析错误原文；进入日志和诊断时只记种类。
type configParseError struct {
	msg string
}

func (e *configParseError) Error() string { return e.msg }

type tomlSkill struct {
	path, name string
	enabled    bool
}

// Tokenize strings and comments so multiline values cannot masquerade as skill tables.
var tomlTokens = regexp.MustCompile(`(?s:"""(?:\\.|[^\\])*?"""|'''.*?''')|"(?:\\.|[^"\\\r\n])*"|'[^'\r\n]*'|#[^\r\n]*|[\[\]{}\n]|[^"'#\[\]{}\n]+`)
var tomlSkillTable = regexp.MustCompile(`^(?:skills|"skills"|'skills')\s*\.\s*(?:config|"config"|'config')$`)

// Read only skill selectors; unrelated TOML values are left uninterpreted.
func parseTOMLSkills(src string) ([]tomlSkill, error) {
	var out []tomlSkill
	section, statement := "", ""
	depth, end := 0, 0
	src += "\n"
	for _, span := range tomlTokens.FindAllStringIndex(src, -1) {
		if span[0] != end {
			return nil, &configParseError{locales.NativeUnterminatedTomlString()}
		}
		end = span[1]
		token := src[span[0]:end]
		if token[0] == '#' {
			continue
		}
		switch token {
		case "[", "{":
			depth++
		case "]", "}":
			depth--
		}
		if depth < 0 {
			return nil, &configParseError{locales.NativeMismatchedTomlBrackets()}
		}
		if token != "\n" || depth != 0 {
			statement += token
			continue
		}
		s := strings.TrimSpace(statement)
		statement = ""
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "[") {
			section = strings.TrimSpace(strings.Trim(s, "[]"))
			if tomlSkillTable.MatchString(section) {
				section = "skills.config"
				if !strings.HasPrefix(s, "[[") {
					return nil, &configParseError{locales.NativeSkillsConfigMustBeTableArray()}
				}
				out = append(out, tomlSkill{enabled: true})
			}
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return nil, &configParseError{locales.NativeTomlParseError(s)}
		}
		key, value = strings.Trim(strings.TrimSpace(key), "\"'"), strings.TrimSpace(value)
		if section == "skills" && key == "config" || section == "" && (key == "skills.config" || key == "skills") {
			return nil, &configParseError{locales.NativeInlineSkillsConfigUnsupported()}
		}
		if section != "skills.config" {
			continue
		}
		v := &out[len(out)-1]
		switch key {
		case "path", "name":
			text, err := strconv.Unquote(value)
			if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
				text, err = value[1:len(value)-1], nil
			}
			if err != nil {
				return nil, &configParseError{err.Error()}
			}
			if key == "path" {
				v.path = text
			} else {
				v.name = text
			}
		case "enabled":
			if value != "true" && value != "false" {
				return nil, &configParseError{locales.NativeEnabledMustBeBoolean()}
			}
			v.enabled = value == "true"
		}
	}
	if end != len(src) || depth != 0 {
		return nil, &configParseError{locales.NativeUnterminatedTomlValue()}
	}
	return out, nil
}

func ownedTOMLBlock(path string) string {
	return "\n# skill-manager: native disable\n[[skills.config]]\npath = " + strconv.Quote(path) + "\nenabled = false\n"
}
func canonicalSkillPath(path string) string {
	if real, err := realPath(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
