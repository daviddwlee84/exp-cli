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
		use: "exp [--skill]", summary: "Use the Git-native research control plane.",
		flags: map[string]string{"skill": "print this build's embedded SKILL.md"},
	},
	"exp agent": {
		use: "exp agent", summary: "Inspect or run configured fresh agent CLI profiles.",
	},
	"exp agent profiles": {
		use: "exp agent profiles [--config PATH] [--json]", summary: "Validate and list local agent CLI profiles.",
		flags: map[string]string{"config": "set the agent profile TOML path", "json": "emit the versioned machine-readable envelope"},
	},
	"exp agent run": {
		use:     "exp agent run --role ROLE --schema PATH [--prompt PATH|-] [--profile NAME] [--cwd DIR] [--config PATH] [--json]",
		summary: "Run one fresh agent CLI with a strict JSON output contract.",
		flags: map[string]string{
			"role": "select the configured role", "schema": "set the JSON Schema file", "prompt": "read the prompt from a file or stdin",
			"profile": "override the role profile", "cwd": "set the agent working directory", "config": "set the agent profile TOML path",
			"json": "emit the versioned machine-readable envelope",
		},
	},
	"exp candidate": {
		use: "exp candidate", summary: "Create scientifically validated, Git-addressed Candidates.",
	},
	"exp candidate create": {
		use: "exp candidate create --experiment ID --evaluation ID --git-commit SHA --change PATH [--json]", summary: "Create a Candidate from supported evidence and an exact change set.",
		flags: map[string]string{"experiment": "select the supported Experiment", "evaluation": "select its passing scientific Evaluation", "git-commit": "pin the full Git object ID", "change": "add an exact changed path", "json": "emit the versioned machine-readable envelope"},
	},
	"exp champion": {
		use: "exp champion [--json]", summary: "Show champions derived from append-only Promotion chains.",
		flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp champion manifest": {
		use: "exp champion manifest [--target TARGET] [--json]", summary: "Render a deterministic downstream manifest from current champions.",
		flags: map[string]string{"target": "select one production target", "json": "emit the versioned machine-readable envelope"},
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
	"exp daemon": {
		use: "exp daemon", summary: "Inspect or control the local orchestration daemon.",
	},
	"exp daemon status": {
		use: "exp daemon status [--json]", summary: "Show local daemon state without provider contact.",
		flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp daemon frontier": {
		use: "exp daemon frontier [--config PATH] [--json]", summary: "Show canonical dispatch frontiers without contacting Pueue.",
		flags: map[string]string{"config": "set the project runtime contract path", "json": "emit the versioned machine-readable envelope"},
	},
	"exp daemon tick": {
		use: "exp daemon tick [--config PATH] [--holder ID] [--json]", summary: "Reconcile Pueue and fill available capacity once.",
		flags: map[string]string{"config": "set the project runtime contract path", "holder": "override the lease holder ID", "json": "emit the versioned machine-readable envelope"},
	},
	"exp daemon run": {
		use: "exp daemon run [--config PATH] [--holder ID] [--interval DURATION]", summary: "Run the local reconcile and admission loop until cancelled.",
		flags: map[string]string{"config": "set the project runtime contract path", "holder": "override the lease holder ID", "interval": "set the reconcile interval"},
	},
	"exp daemon pause": {
		use: "exp daemon pause [--reason TEXT] [--json]", summary: "Pause new dispatch while preserving reconciliation state.",
		flags: map[string]string{"reason": "record a bounded human reason", "json": "emit the versioned machine-readable envelope"},
	},
	"exp daemon resume": {
		use: "exp daemon resume [--reason TEXT] [--json]", summary: "Resume eligible daemon dispatch.",
		flags: map[string]string{"reason": "record a bounded human reason", "json": "emit the versioned machine-readable envelope"},
	},
	"exp init": {
		use: "exp init", summary: "Initialize an idempotent v1 experiments root.",
	},
	"exp evaluation": {
		use: "exp evaluation", summary: "Define comparable protocols and record immutable results.",
	},
	"exp evaluation spec": {
		use: "exp evaluation spec", summary: "Work with scientific and promotion EvaluationSpecs.",
	},
	"exp evaluation spec create": {
		use: "exp evaluation spec create --purpose PURPOSE --dataset NAME --protocol TEXT --metric SPEC --pool ID --budget-hours HOURS [--sealed] [--json]", summary: "Create a comparable EvaluationSpec.",
		flags: map[string]string{"purpose": "select scientific or promotion use", "metric": "declare a metric contract", "pool": "select the budget pool", "sealed": "seal the protocol now", "json": "emit the versioned machine-readable envelope"},
	},
	"exp evaluation create": {
		use: "exp evaluation create --spec ID --subject ID --outcome OUTCOME --metric VALUE [--json]", summary: "Record one immutable Evaluation.",
		flags: map[string]string{"spec": "select the EvaluationSpec", "subject": "select the evaluated subject", "outcome": "set passed, failed, or invalid", "metric": "record a declared metric", "json": "emit the versioned machine-readable envelope"},
	},
	"exp experiment": {
		use: "exp experiment", summary: "Operate isolated experiment workspaces and scientific lifecycle.",
	},
	"exp experiment agent": {
		use: "exp experiment agent EXPERIMENT --base SHA --allow GLOB [--prompt PATH|-] [--profile NAME] [--json]", summary: "Run a fresh code-edit agent in an isolated worktree and commit exact allowlisted changes.",
		flags: map[string]string{"base": "pin the exact base commit", "allow": "allow a changed path glob", "prompt": "supply additional implementation instructions", "profile": "override the experiment_implementer profile", "json": "emit the versioned machine-readable envelope"},
	},
	"exp experiment workspace": {
		use: "exp experiment workspace", summary: "Prepare or commit an allowlisted experiment Git worktree.",
	},
	"exp experiment workspace prepare": {
		use: "exp experiment workspace prepare EXPERIMENT --base SHA --allow GLOB [--json]", summary: "Create an isolated experiment worktree at an exact base commit.",
		flags: map[string]string{"base": "pin the exact base commit", "allow": "allow a changed path glob", "json": "emit the versioned machine-readable envelope"},
	},
	"exp experiment workspace commit": {
		use: "exp experiment workspace commit EXPERIMENT --base SHA --allow GLOB [--json]", summary: "Commit only the exact allowlisted experiment change set.",
		flags: map[string]string{"base": "pin the exact base commit", "allow": "allow a changed path glob", "json": "emit the versioned machine-readable envelope"},
	},
	"exp experiment close": {
		use: "exp experiment close --input PATH|- [--json]", summary: "Atomically conclude an Experiment, complete its Plan, and publish Findings.",
		flags: map[string]string{"input": "read the versioned closure request", "json": "emit the versioned machine-readable envelope"},
	},
	"exp idea": {
		use: "exp idea", summary: "Capture and qualify human or agent research ideas.",
	},
	"exp idea add": {
		use: "exp idea add --title TITLE --summary TEXT [classification flags] [--json]", summary: "Create an unqueued canonical Idea.",
		flags: map[string]string{"title": "set the Idea title", "summary": "state the proposed mechanism", "lane": "classify exploit or explore", "json": "emit the versioned machine-readable envelope"},
	},
	"exp idea list": {
		use: "exp idea list [--json]", summary: "List canonical Ideas.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp idea qualify": {
		use: "exp idea qualify IDEA --resource POOL:UNITS:HOURS [payoff and utility flags] [--json]", summary: "Atomically turn an Idea into a fully priced Plan.",
		flags: map[string]string{"resource": "price constrained pool use", "probability": "estimate probability of improvement", "impact": "estimate impact if successful", "json": "emit the versioned machine-readable envelope"},
	},
	"exp idea develop": {
		use: "exp idea develop IDEA [--profile NAME] [--apply] [--json]", summary: "Ask one fresh agent for a queue-ready Plan proposal.",
		flags: map[string]string{"profile": "override the idea_planner profile", "apply": "atomically qualify the validated proposal", "json": "emit the versioned machine-readable envelope"},
	},
	"exp migrate": {
		use: "exp migrate", summary: "Plan or apply an explicit harness-v0 migration.",
	},
	"exp migrate plan": {
		use: "exp migrate plan [--source DIR] [--resolutions PATH|-] [--output PATH|-] [--json]", summary: "Build a read-only, fingerprinted harness-v0 migration plan.",
		flags: map[string]string{
			"source": "set the Git-root-relative harness-v0 source directory", "resolutions": "read explicit needs_review resolutions from JSON",
			"output": "write the complete no-clobber plan to a path or raw stdout", "json": "emit the versioned machine-readable envelope",
		},
	},
	"exp migrate apply": {
		use: "exp migrate apply --plan PATH|- [--json]", summary: "Apply one fully reviewed harness-v0 migration plan.",
		flags: map[string]string{"plan": "read the exact reviewed migration plan", "json": "emit the versioned machine-readable envelope"},
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
	"exp plan refresh": {
		use: "exp plan refresh PLAN [--json]", summary: "Repin current Finding beliefs and any canonical Queue revision.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp policy": {
		use: "exp policy", summary: "Configure canonical research autonomy and queue policy.",
	},
	"exp policy init": {
		use: "exp policy init [taxonomy and allocation flags] [--json]", summary: "Create an explicit default-manual POLICY.md.",
		flags: map[string]string{"autonomy": "set the autonomy mode", "exploit-share": "set the exploit allocation share", "explore-share": "set the explore allocation share", "confirm-auto-experiment": "explicitly enable assisted or limited dispatch", "json": "emit the versioned machine-readable envelope"},
	},
	"exp policy show": {
		use: "exp policy show [--json]", summary: "Show canonical autonomy and queue policy.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp policy cluster-set": {
		use: "exp policy cluster-set NAME --state STATE [threshold flags] [--json]", summary: "Set cluster saturation thresholds or explicitly reopen a direction.",
		flags: map[string]string{"state": "set open or saturated", "budget-hours": "set the cluster budget", "reopen-condition": "state evidence required to reopen", "expected-revision": "require the exact Policy revision", "json": "emit the versioned machine-readable envelope"},
	},
	"exp policy autonomy": {
		use: "exp policy autonomy MODE [--confirm-auto-experiment] [--json]", summary: "Change autonomy through the explicit auto-experiment gate.",
		flags: map[string]string{"confirm-auto-experiment": "acknowledge assisted or limited dispatch", "expected-revision": "require the exact current Policy revision", "json": "emit the versioned machine-readable envelope"},
	},
	"exp pool": {
		use: "exp pool", summary: "Define constrained compute or human resource pools.",
	},
	"exp pool add": {
		use: "exp pool add --title TITLE --capacity N --unit UNIT --bottleneck SLUG [--json]", summary: "Create a named ResourcePool.",
		flags: map[string]string{"title": "set the pool title", "capacity": "set concurrent capacity", "unit": "name one capacity unit", "json": "emit the versioned machine-readable envelope"},
	},
	"exp pool list": {
		use: "exp pool list [--json]", summary: "List canonical ResourcePools.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp promotion": {
		use: "exp promotion", summary: "Seal and append human-only production promotion decisions.",
	},
	"exp promotion spec-create": {
		use: "exp promotion spec-create --target TARGET --evaluation-spec ID --holdout-budget-hours HOURS [--json]", summary: "Create a sealed human-gated PromotionSpec.",
		flags: map[string]string{"target": "select the production target", "evaluation-spec": "select the sealed holdout protocol", "holdout-budget-hours": "bound holdout use", "json": "emit the versioned machine-readable envelope"},
	},
	"exp promotion append": {
		use: "exp promotion append --target TARGET --spec ID --challenger ID --evaluation ID --outcome OUTCOME --approved-by HUMAN --confirm [--json]", summary: "Append a human Promotion decision.",
		flags: map[string]string{"target": "select the exact target", "challenger": "select the validated Release", "outcome": "set accepted, rejected, or rolled_back", "approved-by": "identify the human approver", "confirm": "confirm the exact production decision", "json": "emit the versioned machine-readable envelope"},
	},
	"exp provider": {
		use: "exp provider", summary: "Run explicit audited reads or controls against supported tools.",
	},
	"exp provider pueue": {
		use: "exp provider pueue", summary: "Inspect or explicitly cancel local Pueue tasks.",
	},
	"exp provider pueue status": {
		use: "exp provider pueue status [--json]", summary: "Read a sanitized Pueue scheduler snapshot.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp provider pueue cancel": {
		use: "exp provider pueue cancel TASK --confirm [--json]", summary: "Explicitly cancel one exact Pueue task.", flags: map[string]string{"confirm": "confirm cancellation", "json": "emit the versioned machine-readable envelope"},
	},
	"exp provider mlflow": {
		use: "exp provider mlflow", summary: "Verify workload-owned MLflow runs without creating them.",
	},
	"exp provider mlflow verify": {
		use: "exp provider mlflow verify --run-id ID [--metric NAME] [--tag NAME=VALUE] [--json]", summary: "Read only requested metrics and tags from an MLflow run.",
		flags: map[string]string{"run-id": "select the workload-owned run", "metric": "request a metric", "tag": "verify one expected tag", "json": "emit the versioned machine-readable envelope"},
	},
	"exp queue": {
		use: "exp queue", summary: "Rank Plans across constrained pool and lane queues.",
	},
	"exp queue create": {
		use: "exp queue create --pool ID [--json]", summary: "Create exploit and explore partitions for ResourcePools.", flags: map[string]string{"pool": "add a ResourcePool", "json": "emit the versioned machine-readable envelope"},
	},
	"exp queue list": {
		use: "exp queue list [--json]", summary: "List canonical Queues.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp queue show": {
		use: "exp queue show QUEUE [--json]", summary: "Show ordered pool/lane entries and pinned revisions.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp queue insert": {
		use: "exp queue insert QUEUE PLAN [--pool ID] [--agent] [--json]", summary: "Score and insert a Plan with optional listwise advice and battles.",
		flags: map[string]string{"pool": "select the constrained ResourcePool", "agent": "run listwise advice and order-swapped battles", "position": "override the provisional position", "score": "override the transparent score", "pin": "human-pin or override cluster saturation", "json": "emit the versioned machine-readable envelope"},
	},
	"exp queue remove": {
		use: "exp queue remove QUEUE PLAN [--json]", summary: "Remove a Plan with an exact Queue CAS.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp record": {
		use: "exp record", summary: "Inspect or atomically apply canonical records.",
	},
	"exp record list": {
		use: "exp record list [--kind KIND] [--json]", summary: "List Git-backed canonical records.", flags: map[string]string{"kind": "filter by canonical kind", "json": "emit the versioned machine-readable envelope"},
	},
	"exp record show": {
		use: "exp record show REF [--raw|--json]", summary: "Show one canonical record.", flags: map[string]string{"raw": "emit normalized canonical Markdown", "json": "emit the versioned machine-readable envelope"},
	},
	"exp record transaction": {
		use: "exp record transaction --input PATH|- [--json]", summary: "Apply a low-risk Idea/ResourcePool prepared transaction.", flags: map[string]string{"input": "read the transaction request", "json": "emit the versioned machine-readable envelope"},
	},
	"exp record recover": {
		use: "exp record recover [--json]", summary: "Roll durable prepared transactions forward.", flags: map[string]string{"json": "emit the versioned machine-readable envelope"},
	},
	"exp release": {
		use: "exp release", summary: "Assemble typed Candidate slots for downstream targets.",
	},
	"exp release create": {
		use: "exp release create --input PATH|- [--json]", summary: "Create a draft or atomically validated typed Release.", flags: map[string]string{"input": "read the versioned Release request", "json": "emit the versioned machine-readable envelope"},
	},
	"exp render": {
		use: "exp render [--check]", summary: "Render deterministic projections or check them without writing.",
		flags: map[string]string{"check": "report projection drift without writing"},
	},
	"exp skill": {
		use: "exp skill print|install|check|sync", summary: "Inspect or manage the version-matched embedded guidance skill.",
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
	"exp skill sync": {
		use: "exp skill sync [--check]", summary: "Synchronize the generated source-tree command reference for development.",
		flags: map[string]string{"check": "report source-tree command-reference drift without writing"},
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
