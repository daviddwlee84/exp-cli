package skill_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/daviddwlee84/exp-cli/internal/cli"
	"github.com/daviddwlee84/exp-cli/internal/skill"
)

func TestRenderFilesAndManifestAreDeterministic(t *testing.T) {
	first, err := skill.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	second, err := skill.Files()
	if err != nil {
		t.Fatalf("Files again: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Files changed between calls")
	}

	for _, expected := range []string{
		"SKILL.md",
		"references/commands.md",
		"references/external-tools.md",
		"references/methodology.md",
		"references/records-and-project-knowledge.md",
		"references/usage-and-fallback.md",
	} {
		if _, ok := first[expected]; !ok {
			t.Errorf("missing embedded file %s", expected)
		}
	}

	rendered, err := skill.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rendered != string(first["SKILL.md"]) {
		t.Fatal("Render did not return embedded SKILL.md")
	}
	first["SKILL.md"][0] = 'x'
	renderedAgain, err := skill.Render()
	if err != nil {
		t.Fatalf("Render after caller mutation: %v", err)
	}
	if renderedAgain != rendered {
		t.Fatal("caller mutation changed embedded content")
	}

	manifestA, err := skill.Manifest()
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	manifestB, err := skill.Manifest()
	if err != nil {
		t.Fatalf("Manifest again: %v", err)
	}
	if !reflect.DeepEqual(manifestA, manifestB) {
		t.Fatal("manifest changed between calls")
	}
	if manifestA.Name != skill.Name || manifestA.SchemaVersion != skill.SchemaVersion || manifestA.SkillVersion != skill.SkillVersion {
		t.Fatalf("unexpected manifest metadata: %#v", manifestA)
	}
	if !strings.HasPrefix(manifestA.Hash, "sha256:") || len(manifestA.Hash) != len("sha256:")+64 {
		t.Fatalf("unexpected manifest hash %q", manifestA.Hash)
	}
	for index := 1; index < len(manifestA.Files); index++ {
		if manifestA.Files[index-1].Path >= manifestA.Files[index].Path {
			t.Fatalf("manifest is not path-sorted: %#v", manifestA.Files)
		}
	}
	hash, err := skill.ContentHash()
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if hash != manifestA.Hash {
		t.Fatalf("ContentHash = %q, manifest hash = %q", hash, manifestA.Hash)
	}
}

func TestEmbeddedSkillFrontmatterAndReferences(t *testing.T) {
	files, err := skill.Files()
	if err != nil {
		t.Fatal(err)
	}
	body := string(files["SKILL.md"])
	for _, required := range []string{
		"---\nname: exp-cli\n",
		"schema-version: \"" + skill.SchemaVersion + "\"",
		"skill-version: \"" + skill.SkillVersion + "\"",
		"references/commands.md",
		"references/methodology.md",
		"references/records-and-project-knowledge.md",
		"references/usage-and-fallback.md",
		"references/external-tools.md",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("SKILL.md is missing %q", required)
		}
	}

	for _, phrase := range []string{
		"expected payoff", "design", "comparable evidence", "negative", "Operational failure",
		"TODO", "backlog", "pitfall", "invariant", "read-only fallback",
		"DVC", "MLflow", "Pueue", "Slurm", "Marimo",
	} {
		found := false
		for _, content := range files {
			if strings.Contains(strings.ToLower(string(content)), strings.ToLower(phrase)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("embedded guidance does not cover %q", phrase)
		}
	}
}

func TestCommandReferenceGenerationAndEmbeddedCheck(t *testing.T) {
	metadata, err := cli.CommandMetadata(cli.NewRootCommand(cli.NewApp(t.Context(), nil, nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(metadata)
	for index := range metadata {
		slices.Reverse(metadata[index].Flags)
	}
	first, err := skill.GenerateCommandReference(metadata)
	if err != nil {
		t.Fatalf("GenerateCommandReference: %v", err)
	}
	slices.Reverse(metadata)
	second, err := skill.GenerateCommandReference(metadata)
	if err != nil {
		t.Fatalf("GenerateCommandReference reordered: %v", err)
	}
	if first != second {
		t.Fatal("command reference depends on metadata order")
	}
	if !strings.HasSuffix(first, "\n") || strings.HasSuffix(first, "\n\n") {
		t.Fatal("command reference must end in exactly one LF")
	}
	check, err := skill.CheckEmbeddedCommandReference(metadata)
	if err != nil {
		t.Fatalf("CheckEmbeddedCommandReference: %v", err)
	}
	if !check.Current {
		t.Fatalf("embedded command reference drifted: expected %s, actual %s", check.ExpectedHash, check.ActualHash)
	}
	if check.Expected != first {
		t.Fatal("check did not return deterministic expected content")
	}

	drifted, err := skill.CheckCommandReference(metadata, []byte(first+"unexpected\n"))
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Current || drifted.ExpectedHash == drifted.ActualHash {
		t.Fatal("command-reference drift was not detected")
	}
}

func TestCommandReferenceRejectsDuplicateMetadata(t *testing.T) {
	_, err := skill.GenerateCommandReference([]skill.CommandMetadata{
		{Path: "exp init", Use: "exp init", Summary: "one"},
		{Path: "exp init", Use: "exp init", Summary: "two"},
	})
	if err == nil {
		t.Fatal("duplicate command paths should fail")
	}
}

func TestCommandReferenceRejectsRenderedMetadataInjection(t *testing.T) {
	base := skill.CommandMetadata{
		Path:    "exp example",
		Use:     "exp example [left|right]",
		Summary: "Describe the example safely.",
		Flags: []skill.FlagMetadata{
			{Name: "format", Usage: "select the output format"},
		},
	}
	tests := []struct {
		name   string
		mutate func(*skill.CommandMetadata)
	}{
		{name: "summary newline", mutate: func(command *skill.CommandMetadata) { command.Summary = "safe\n| injected |" }},
		{name: "summary trailing newline", mutate: func(command *skill.CommandMetadata) { command.Summary += "\n" }},
		{name: "summary table", mutate: func(command *skill.CommandMetadata) { command.Summary = "safe | injected" }},
		{name: "summary ATX heading", mutate: func(command *skill.CommandMetadata) { command.Summary = "# forged heading" }},
		{name: "summary unordered list", mutate: func(command *skill.CommandMetadata) { command.Summary = "- forged item" }},
		{name: "summary ordered list", mutate: func(command *skill.CommandMetadata) { command.Summary = "1. forged item" }},
		{name: "summary thematic break", mutate: func(command *skill.CommandMetadata) { command.Summary = "* * *" }},
		{name: "summary link", mutate: func(command *skill.CommandMetadata) { command.Summary = "[forged](https://example.com)" }},
		{name: "summary javascript link", mutate: func(command *skill.CommandMetadata) { command.Summary = "[forged](javascript:alert(1))" }},
		{name: "summary reference link", mutate: func(command *skill.CommandMetadata) { command.Summary = "[forged][target]" }},
		{name: "summary link definition", mutate: func(command *skill.CommandMetadata) { command.Summary = "[target]: javascript:alert(1)" }},
		{name: "summary image", mutate: func(command *skill.CommandMetadata) { command.Summary = "![forged](https://example.com/image.png)" }},
		{name: "summary markdown control", mutate: func(command *skill.CommandMetadata) { command.Summary = "`injected`" }},
		{name: "summary html control", mutate: func(command *skill.CommandMetadata) { command.Summary = "<table>" }},
		{name: "usage newline", mutate: func(command *skill.CommandMetadata) { command.Flags[0].Usage = "safe\n| injected |" }},
		{name: "usage table", mutate: func(command *skill.CommandMetadata) { command.Flags[0].Usage = "safe | injected" }},
		{name: "usage link", mutate: func(command *skill.CommandMetadata) { command.Flags[0].Usage = "read [details](javascript:alert(1))" }},
		{name: "usage image", mutate: func(command *skill.CommandMetadata) {
			command.Flags[0].Usage = "show ![badge](https://example.com/badge.png)"
		}},
		{name: "usage markdown control", mutate: func(command *skill.CommandMetadata) { command.Flags[0].Usage = "`injected`" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := base
			command.Flags = append([]skill.FlagMetadata(nil), base.Flags...)
			test.mutate(&command)
			if _, err := skill.GenerateCommandReference([]skill.CommandMetadata{command}); err == nil {
				t.Fatal("unsafe rendered metadata was accepted")
			}
		})
	}

	if _, err := skill.GenerateCommandReference([]skill.CommandMetadata{base}); err != nil {
		t.Fatalf("legitimate pipe notation in fenced command use was rejected: %v", err)
	}
}
