package cli

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestEmbeddedCommandReferenceMatchesActualCobraTree(t *testing.T) {
	root := NewRootCommand(NewApp(t.Context(), nil, nil, nil))
	check, err := CheckEmbeddedCommandReference(root)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Current {
		t.Fatalf("embedded command reference drifted: expected %s, actual %s\n--- expected ---\n%s", check.ExpectedHash, check.ActualHash, check.Expected)
	}
}

func TestApprovedMetadataCoversLiveMachineAndCheckFlags(t *testing.T) {
	root := NewRootCommand(NewApp(t.Context(), nil, nil, nil))
	for path, command := range commandTreeByPath(root) {
		if command.Hidden || command.Name() == "help" || command.Name() == "completion" || command.Annotations[commandReferenceExcludedAnnotation] == "true" {
			continue
		}
		spec := approvedCommandReference[path]
		command.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden || flag.Name != "json" && flag.Name != "check" {
				return
			}
			if _, found := spec.flags[flag.Name]; !found {
				t.Errorf("approved metadata for %s omits live --%s", path, flag.Name)
			}
		})
	}
}

func TestRequiredLifecycleFieldsAppearInApprovedSyntax(t *testing.T) {
	for path, required := range map[string][]string{
		"exp evaluation spec create": {"title"},
		"exp evaluation create":      {"title", "summary"},
		"exp candidate create":       {"title"},
		"exp promotion spec-create":  {"title"},
		"exp promotion append":       {"title"},
	} {
		spec := approvedCommandReference[path]
		for _, name := range required {
			if !strings.Contains(spec.use, "--"+name) {
				t.Errorf("%s syntax omits required --%s", path, name)
			}
		}
	}
}

func TestCommandReferenceContainsAutonomousResearchWorkflow(t *testing.T) {
	reference, err := GenerateCommandReference(NewRootCommand(NewApp(t.Context(), nil, nil, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"## `exp init`", "## `exp doctor`", "## `exp plan add`", "## `exp plan list`",
		"## `exp validate`", "## `exp render`", "## `exp context`", "## `exp skill print`",
		"## `exp skill install`", "## `exp skill check`", "## `exp skill sync`",
		"## `exp migrate plan`", "## `exp migrate apply`",
		"## `exp idea develop`", "## `exp queue insert`", "## `exp daemon tick`",
		"## `exp experiment close`", "## `exp candidate create`", "## `exp release create`",
		"## `exp promotion append`", "## `exp champion manifest`",
		"## `exp workspace register`", "## `exp workspace status`",
		"## `exp source add`", "## `exp source list`", "## `exp source show`", "## `exp source status`", "## `exp source register`", "## `exp source append-locator`", "## `exp source retire`",
		"## `exp config path`", "## `exp config show`", "## `exp config explain`", "## `exp config trust`", "## `exp config revoke`", "## `exp config list`",
	} {
		if !strings.Contains(reference, path) {
			t.Errorf("generated reference is missing %s", path)
		}
	}
	for _, deferred := range []string{"## `exp run"} {
		if strings.Contains(reference, deferred) {
			t.Errorf("generated reference advertises deferred command %s", deferred)
		}
	}
}
