// i18nextract finds Chinese UI strings and applies a reviewed migration plan.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type placeholder struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type entry struct {
	ID           string        `json:"id"`
	File         string        `json:"file"`
	Line         int           `json:"line"`
	Col          int           `json:"col"`
	Kind         string        `json:"kind"`
	GoText       string        `json:"goText"`
	Zh           string        `json:"zh"`
	Args         []string      `json:"args"`
	Placeholders []placeholder `json:"placeholders"`
	Key          string        `json:"key"`
	Reason       string        `json:"reason"`
	Wrapped      string        `json:"wrapped,omitempty"`
	En           *string       `json:"en,omitempty"`
	Description  string        `json:"description,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "i18nextract:", err)
		os.Exit(1)
	}
}
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: i18nextract scan -prefix NAME FILE... | apply -plan FILE -catalog DIR [-generate]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	switch args[0] {
	case "scan":
		prefix := flags.String("prefix", "", "catalog key prefix")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() == 0 {
			return errors.New("scan requires at least one Go file")
		}
		entries, err := scanFiles(*prefix, flags.Args())
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(entries)
	case "apply":
		plan := flags.String("plan", "", "reviewed scan JSON")
		catalog := flags.String("catalog", "", "catalog directory")
		generate := flags.Bool("generate", false, "run go tool mygo generate after applying")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *plan == "" || *catalog == "" || flags.NArg() != 0 {
			return errors.New("apply requires -plan and -catalog, without positional arguments")
		}
		data, err := os.ReadFile(*plan)
		if err != nil {
			return err
		}
		var entries []entry
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("plan: %w", err)
		}
		if err := applyEntries(entries, *catalog, stdout); err != nil {
			return err
		}
		if *generate {
			dir, err := filepath.Abs(filepath.Dir(filepath.Clean(*catalog)))
			if err != nil {
				return err
			}
			cmd := exec.Command("go", "tool", "mygo", "generate")
			cmd.Dir = dir
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("files applied, but generation failed: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
