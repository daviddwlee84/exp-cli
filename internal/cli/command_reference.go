package cli

import (
	"fmt"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/skill"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type commandReferenceSpec struct {
	use     string
	summary string
	flags   map[string]string
}

var approvedCommandReference = map[string]commandReferenceSpec{
	"exp": {
		use: "exp", summary: "Use the Git-native research control plane.",
	},
	"exp context": {
		use: "exp context [--json]", summary: "Show a local, resumable research summary without provider refresh.",
		flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp doctor": {
		use: "exp doctor [--json] [--live]", summary: "Inspect local core and optional-tool capabilities.",
		flags: map[string]string{
			"json": "emit the versioned machine-readable envelope",
			"live": "permit only the explicitly documented live probes",
		},
	},
	"exp init": {
		use: "exp init", summary: "Initialize an idempotent v1 experiments root.",
	},
	"exp plan": {
		use: "exp plan", summary: "Work with priced research Plans.",
	},
	"exp plan add": {
		use: "exp plan add [flags | --input -] [--json]", summary: "Create one validated Plan from human flags or versioned JSON input.",
		flags: map[string]string{
			"input": "read the versioned Plan request from standard input (must be -)",
			"json":  "emit the versioned machine-readable envelope",
		},
	},
	"exp plan list": {
		use: "exp plan list [--json]", summary: "List canonical Plans without contacting providers.",
		flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp render": {
		use: "exp render [--check]", summary: "Render deterministic projections or check them without writing.",
		flags: map[string]string{"check": "report projection drift without writing"},
	},
	"exp skill": {
		use: "exp skill print|install|check", summary: "Inspect or manage the version-matched embedded guidance skill.",
	},
	"exp skill check": {
		use: "exp skill check", summary: "Check installed files, compatibility, manifest hash, and consumer links without mutation.",
	},
	"exp skill install": {
		use: "exp skill install", summary: "Atomically install the embedded skill and safe consumer links.",
	},
	"exp skill print": {
		use: "exp skill print", summary: "Print this build's embedded SKILL.md.",
	},
	"exp validate": {
		use: "exp validate [--json]", summary: "Validate canonical local records without provider calls.",
		flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
}

// CommandMetadata traverses the actual visible Cobra tree into the cycle-free
// value contract accepted by the embedded skill package. The approved reference
// intentionally lists primary machine/check flags rather than duplicating full
// Cobra help; every referenced flag is verified to exist on its live command.
func CommandMetadata(root *cobra.Command) ([]skill.CommandMetadata, error) {
	if root == nil {
		return nil, fmt.Errorf("command root is nil")
	}
	metadata := make([]skill.CommandMetadata, 0, len(approvedCommandReference))
	seen := make(map[string]struct{}, len(approvedCommandReference))
	var visit func(*cobra.Command) error
	visit = func(command *cobra.Command) error {
		if command == nil || command.Hidden || command.Name() == "help" || command.Name() == "completion" {
			return nil
		}
		path := command.CommandPath()
		spec, approved := approvedCommandReference[path]
		if !approved {
			return fmt.Errorf("visible command %q has no approved command-reference metadata", path)
		}
		seen[path] = struct{}{}
		flags, err := commandReferenceFlags(command, spec.flags)
		if err != nil {
			return err
		}
		metadata = append(metadata, skill.CommandMetadata{
			Path: path, Use: spec.use, Summary: spec.summary, Flags: flags,
		})
		for _, child := range command.Commands() {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	for path := range approvedCommandReference {
		if _, found := seen[path]; !found {
			return nil, fmt.Errorf("approved command %q is absent from the Cobra tree", path)
		}
	}
	return metadata, nil
}

// GenerateCommandReference renders commands.md from the actual Cobra tree.
func GenerateCommandReference(root *cobra.Command) (string, error) {
	metadata, err := CommandMetadata(root)
	if err != nil {
		return "", err
	}
	return skill.GenerateCommandReference(metadata)
}

// CheckEmbeddedCommandReference verifies that this build's command tree and
// embedded references/commands.md have exact matching bytes.
func CheckEmbeddedCommandReference(root *cobra.Command) (skill.CommandReferenceCheck, error) {
	metadata, err := CommandMetadata(root)
	if err != nil {
		return skill.CommandReferenceCheck{}, err
	}
	return skill.CheckEmbeddedCommandReference(metadata)
}

func commandReferenceFlags(command *cobra.Command, approved map[string]string) ([]skill.FlagMetadata, error) {
	flagsByName := make(map[string]*pflag.Flag)
	add := func(flag *pflag.Flag) {
		if flag != nil && !flag.Hidden {
			flagsByName[flag.Name] = flag
		}
	}
	command.NonInheritedFlags().VisitAll(add)
	command.PersistentFlags().VisitAll(add)
	flags := make([]skill.FlagMetadata, 0, len(approved))
	for name, usage := range approved {
		flag, found := flagsByName[name]
		if !found {
			return nil, fmt.Errorf("command %q is missing referenced flag --%s", command.CommandPath(), name)
		}
		flags = append(flags, skill.FlagMetadata{
			Name: name, Shorthand: strings.TrimSpace(flag.Shorthand), Usage: usage,
		})
	}
	return flags, nil
}
