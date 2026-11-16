package library

import (
	"reflect"
	"testing"
)

func TestTOMLReadsSkillSelectors(t *testing.T) {
	src := `# keep
model = "x"
[[skills.config]]
name = 'user-choice' # selector
"enabled" = false
[[skills.config]]
path = "/skills/with # hash/SKILL.md"
name = "alias"
[[skills.config]]
path = '/another/SKILL.md'
enabled = true
[other]
name = "not a skill"
enabled = false
`
	got, err := parseTOMLSkills(src)
	want := []tomlSkill{{name: "user-choice", enabled: false}, {path: "/skills/with # hash/SKILL.md", name: "alias", enabled: true}, {path: "/another/SKILL.md", enabled: true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("selectors: %+v, %v", got, err)
	}
}

func TestTOMLRejectsUnreadableSkillConfiguration(t *testing.T) {
	for _, src := range []string{
		"model = \"unterminated", "not toml", "[bad", "a = [1,",
		"skills = { config = [] }", "skills.config = []", "[skills]\nconfig = []", "[skills.config]",
		"[[skills.config]]\nenabled = 'false'", "[[skills.config]]\npath = 123",
	} {
		if _, err := parseTOMLSkills(src); err == nil {
			t.Errorf("accepted unreadable configuration: %q", src)
		}
	}
}

func TestTOMLIgnoresUnrelatedLegalValues(t *testing.T) {
	src := `model = """multiline
[[skills.config]]
name = 'not-a-real-skill'
enabled = false
"""
literal = '''another
[[skills.config]]
enabled = false
'''
["future.section"]
values = [
  { nested = ["one", "two"] }, # keep
]
[future.section]
value = 1979-05-27T07:32:00Z
[[skills.config]]
name = "actual"
enabled = true
extra = { date = 1979-05-27, number = 1_000.25 }
`
	got, err := parseTOMLSkills(src)
	if err != nil || !reflect.DeepEqual(got, []tomlSkill{{name: "actual", enabled: true}}) {
		t.Fatalf("unrelated TOML interfered with selectors: %+v, %v", got, err)
	}
}

func TestTOMLReadsSpacedAndQuotedSkillTables(t *testing.T) {
	for _, header := range []string{`[[ skills . config ]]`, `[["skills".'config']]`} {
		got, err := parseTOMLSkills(header + "\nname = 'demo'\nenabled = false\n")
		if err != nil || !reflect.DeepEqual(got, []tomlSkill{{name: "demo", enabled: false}}) {
			t.Fatalf("missed skill table %s: %+v, %v", header, got, err)
		}
	}
}
