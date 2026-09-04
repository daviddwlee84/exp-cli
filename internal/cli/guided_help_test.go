package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlowCatalogReferencesExistingFamiliesAndGuides(t *testing.T) {
	root := NewRootCommand(NewApp(t.Context(), nil, nil, nil))
	if err := validateFlowHelpCatalog(root); err != nil {
		t.Fatal(err)
	}
	for name, diagram := range flowMaps {
		if !isASCII(diagram) {
			t.Errorf("flow map %q is not ASCII", name)
		}
	}
	for path, annotation := range flowHelpAnnotations {
		if !strings.Contains(commandTreeByPath(root)[path].Long, flowMaps[annotation.flow]) {
			t.Errorf("command %q was not annotated from flow map %q", path, annotation.flow)
		}
		if annotation.guide != "" && !strings.Contains(commandTreeByPath(root)[path].Long, "exp guide "+annotation.guide) {
			t.Errorf("command %q is missing guide pointer %q", path, annotation.guide)
		}
	}
}

func TestRootAndFamilyHelpCarryConciseFlowMaps(t *testing.T) {
	cases := []struct {
		args   []string
		marker string
		guide  string
	}{
		{[]string{"--help"}, "set up authority, then choose the evidence level", "exp guide setup"},
		{[]string{"source", "--help"}, "bind identity, then observe local clones", "exp guide workspaces"},
		{[]string{"try", "--help"}, "bounded exploration before formal research", "exp guide quick"},
		{[]string{"queue", "--help"}, "transparent ordering under constrained pools", "exp guide research"},
		{[]string{"promotion", "--help"}, "production remains a named human decision", "exp guide promotion"},
		{[]string{"provider", "--help"}, "explicit narrow operations keep upstream ownership", "exp guide config"},
	}
	for _, testCase := range cases {
		invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", testCase.args...)
		if invocation.err != nil {
			t.Fatalf("%v: %v", testCase.args, invocation.err)
		}
		if !strings.Contains(invocation.stdout, testCase.marker) || !strings.Contains(invocation.stdout, testCase.guide) {
			t.Errorf("%v help lacks flow context:\n%s", testCase.args, invocation.stdout)
		}
	}
}

func TestGuideTopicsAreEmbeddedResolvableAndTerminalOnlyStyled(t *testing.T) {
	index := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "guide")
	if index.err != nil {
		t.Fatal(index.err)
	}
	for _, topic := range guideTopicNames() {
		if !strings.Contains(index.stdout, topic) {
			t.Errorf("guide index is missing %q", topic)
		}
		plain := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "--color", "never", "guide", topic)
		colored := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "--color", "always", "guide", topic)
		if plain.err != nil || colored.err != nil {
			t.Errorf("guide %s: plain=%v colored=%v", topic, plain.err, colored.err)
			continue
		}
		if !strings.HasPrefix(plain.stdout, "# ") || !strings.Contains(colored.stdout, "\x1b[") {
			t.Errorf("guide %s was not rendered as expected", topic)
		}
		if got := stripANSI(colored.stdout); got != plain.stdout {
			t.Errorf("guide %s terminal styling reflowed Markdown", topic)
		}
	}
}

func TestStandardHelpCommandIsNotReplacedByGuide(t *testing.T) {
	invocation := invokeCommand(t, NewApp(t.Context(), nil, nil, nil), "", "help", "source")
	if invocation.err != nil {
		t.Fatal(invocation.err)
	}
	if !strings.Contains(invocation.stdout, "Usage:") || !strings.Contains(invocation.stdout, "Available Commands:") {
		t.Fatalf("exp help source no longer renders Cobra help:\n%s", invocation.stdout)
	}
	if strings.HasPrefix(invocation.stdout, "# Resolve workspaces") {
		t.Fatal("standard help was hijacked by a conceptual guide")
	}
}

func TestUnknownCommandsUseSuggestionsAndTargetedHelpWithoutUsageDump(t *testing.T) {
	for _, testCase := range []struct {
		args       []string
		suggestion string
		pointer    string
	}{
		{[]string{"pla"}, "plan", `Run "exp --help"`},
		{[]string{"source", "lis"}, "list", `Run "exp source --help"`},
		{[]string{"completion", "bas"}, "bash", `Run "exp completion --help"`},
		{[]string{"help", "srouce"}, "source", `Run "exp --help"`},
		{[]string{"help", "source", "lis"}, "list", `Run "exp source --help"`},
	} {
		var stdout, stderr bytes.Buffer
		code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, testCase.args)
		if code != 1 || stdout.Len() != 0 {
			t.Fatalf("%v: code=%d stdout=%q", testCase.args, code, stdout.String())
		}
		message := stripANSI(stderr.String())
		if !strings.Contains(message, "Did you mean this?") || !strings.Contains(message, testCase.suggestion) || !strings.Contains(message, testCase.pointer) {
			t.Errorf("%v lost suggestion or pointer: %q", testCase.args, message)
		}
		if strings.Contains(message, "Usage:") || strings.Contains(message, "Available Commands:") {
			t.Errorf("%v emitted a giant usage dump: %q", testCase.args, message)
		}
	}
}

func TestUnknownSubcommandJSONHasOneFailureEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"source", "lis", "--json"})
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Count(stdout.String(), "\n") != 1 || strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("JSON stdout is not one envelope: %q", stdout.String())
	}
	envelope := decodeEnvelope(t, stdout.String())
	if envelope.OK || envelope.Command != "source" || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "command.invalid_usage" {
		t.Fatalf("failure envelope = %#v", envelope)
	}
	for _, wanted := range []string{`unknown command "lis"`, "Did you mean this?", "list", `Run "exp source --help"`} {
		if !strings.Contains(envelope.Diagnostics[0].Message, wanted) || !strings.Contains(stderr.String(), wanted) {
			t.Errorf("JSON unknown-subcommand diagnostics omitted %q: envelope=%q stderr=%q", wanted, envelope.Diagnostics[0].Message, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "Usage:") || strings.Contains(envelope.Diagnostics[0].Message, "unknown flag: --json") {
		t.Fatalf("JSON unknown-subcommand diagnostics were masked: envelope=%q stderr=%q", envelope.Diagnostics[0].Message, stderr.String())
	}
}

func TestUnknownRootCommandJSONPreservesSuggestion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(t.Context(), strings.NewReader(""), &stdout, &stderr, []string{"pla", "--json"})
	if code != 1 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("root typo JSON: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	envelope := decodeEnvelope(t, stdout.String())
	if envelope.OK || envelope.Command != "exp" || len(envelope.Diagnostics) != 1 {
		t.Fatalf("failure envelope = %#v", envelope)
	}
	for _, wanted := range []string{`unknown command "pla"`, "Did you mean this?", "plan", `Run "exp --help"`} {
		if !strings.Contains(envelope.Diagnostics[0].Message, wanted) || !strings.Contains(stderr.String(), wanted) {
			t.Errorf("root JSON diagnostics omitted %q: envelope=%q stderr=%q", wanted, envelope.Diagnostics[0].Message, stderr.String())
		}
	}
	if strings.Contains(envelope.Diagnostics[0].Message, "unknown flag: --json") || strings.Contains(stdout.String()+stderr.String(), "Usage:") {
		t.Fatalf("root JSON typo was masked or flooded usage: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
