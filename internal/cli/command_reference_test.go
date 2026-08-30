package cli

import (
	"strings"
	"testing"
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
