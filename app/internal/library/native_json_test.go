package library

import (
	"strings"
	"testing"
)

func TestJSONLocalEdits(t *testing.T) {
	tests := []struct {
		name, src   string
		path        []string
		value, want string
	}{
		{"new parents", "{\n  \"theme\": \"dark\"\n}\n", []string{"permission", "skill", "x"}, `"deny"`, "{\n  \"theme\": \"dark\",\n  \"permission\": {\n    \"skill\": {\n      \"x\": \"deny\"\n    }\n  }\n}\n"},
		{"existing parent", "{\n\t\"z\": 1,\n\t\"skillOverrides\": {\n\t\t\"other\": \"on\"\n\t},\n\t\"a\": 2\n}", []string{"skillOverrides", "x"}, `"off"`, "{\n\t\"z\": 1,\n\t\"skillOverrides\": {\n\t\t\"other\": \"on\",\n\t\t\"x\": \"off\"\n\t},\n\t\"a\": 2\n}"},
		{"comments and trailing comma", "{\n // keep\n \"permission\": {\"skill\": {\"x\": \"ask\",}},\n \"tail\": true,\n}", []string{"permission", "skill", "x"}, `"deny"`, "{\n // keep\n \"permission\": {\"skill\": {\"x\": \"deny\",}},\n \"tail\": true,\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := editJSON(tt.src, tt.path, &tt.value)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestJSONDeleteAndPruneOwnedParents(t *testing.T) {
	original := "{\n  \"other\": 1\n}\n"
	value := `"off"`
	got, err := editJSON(original, []string{"skillOverrides", "x"}, &value)
	if err != nil {
		t.Fatal(err)
	}
	got, err = editJSON(got, []string{"skillOverrides", "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = pruneJSON(got, [][]string{{"skillOverrides"}})
	if err != nil || got != original {
		t.Fatalf("%q %v", got, err)
	}
	src := `{"before": 1, "skillOverrides": {"x": "off", "other": "on"}, "after": 2}`
	got, err = editJSON(src, []string{"skillOverrides", "x"}, nil)
	if err != nil || !strings.Contains(got, `"other": "on"`) || strings.Contains(got, `"x"`) {
		t.Fatalf("%s %v", got, err)
	}
	got, err = pruneJSON(`{"skillOverrides": {}}`, nil)
	if err != nil || got != `{"skillOverrides": {}}` {
		t.Fatal("user empty object removed")
	}
}

func TestJSONRejectsMalformedOrAmbiguousConfig(t *testing.T) {
	value := `"off"`
	for _, src := range []string{`{"x":`, `{"skillOverrides": 1}`, `{"skillOverrides":{},"skillOverrides":{}}`, `{"x":true /*`} {
		if _, err := editJSON(src, []string{"skillOverrides", "s"}, &value); err == nil {
			t.Errorf("accepted %s", src)
		}
	}
}

func TestJSONMultipleTargetsAndCommentsSurviveDeletion(t *testing.T) {
	for _, src := range []string{
		"{\n  \"a\": 1, // a\n  \"skillOverrides\": {\n    \"x\": \"off\",\n    \"y\": \"off\",\n  },\n  \"tail\": 2\n}\n",
		`{"skillOverrides":{"x":"off","y":"off",},"tail":2}`,
	} {
		got, err := editJSON(src, []string{"skillOverrides", "y"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseJSON(got); err != nil {
			t.Fatalf("invalid after deletion: %s: %v", got, err)
		}
		got, err = editJSON(got, []string{"skillOverrides", "x"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseJSON(got); err != nil {
			t.Fatalf("invalid after last deletion: %s: %v", got, err)
		}
		if strings.Contains(src, "// a") && !strings.Contains(got, "// a") {
			t.Fatal("deleted unrelated comment")
		}
	}
}

func TestJSONRoundTripPreservesUnrelatedTrailingComment(t *testing.T) {
	for _, src := range []string{"{\n  \"keep\": 1 // keep, this comment\n}\n", "{\n  \"keep\": 1, // keep, this comment\n}\n"} {
		v := `"off"`
		got, err := editJSON(src, []string{"skillOverrides", "x"}, &v)
		if err != nil {
			t.Fatal(err)
		}
		got, err = editJSON(got, []string{"skillOverrides", "x"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err = pruneJSON(got, [][]string{{"skillOverrides"}})
		if err != nil || !strings.Contains(got, "// keep, this comment") {
			t.Fatalf("comment lost: %q %v", got, err)
		}
		if _, err := parseJSON(got); err != nil {
			t.Fatal(got, err)
		}
	}
}

func TestJSONArrayRoundTripPreservesInlineTrivia(t *testing.T) {
	for _, src := range []string{
		`{"disabled":["other",]}`,
		`{"disabled":["other" /* belongs to other */, /* trailing */]}`,
		`{"disabled":["other" /* belongs to other */]}`,
		`{"disabled":[ /* empty list */ ]}`,
	} {
		t.Run(src, func(t *testing.T) {
			got, err := editJSONArray(src, []string{"disabled"}, "demo", false)
			must(t, err)
			got, err = editJSONArray(got, []string{"disabled"}, "demo", true)
			must(t, err)
			if got != src {
				t.Fatalf("lost array trivia: %s", got)
			}
		})
	}
}
