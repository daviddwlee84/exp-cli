package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// flowMaps is the single source for the concise orientation blocks attached to
// Cobra help. Keep these diagrams 7-bit ASCII so they remain legible in every
// terminal exp supports.
var flowMaps = map[string]string{
	"root": `FLOW: set up authority, then choose the evidence level

  init -> workspace/source/config -> choose
                                  |-> try -> finish/adopt -> Idea
                                  +-> Idea -> Plan -> Queue -> Experiment
                                                            |
  manifest <- Promotion <- Release <- Candidate <- Evaluation`,
	"setup": `FLOW: establish canonical authority before execution

  init -> workspace register -> source add/register -> config show/trust
    |                                                        |
    +------------------ validate <---------------------------+
                               |
                               +-> choose Try or Experiment`,
	"source": `FLOW: bind identity, then observe local clones

  source add -> canonical Source -> local association -> source status
                                      |
  append-locator / retire ------------+-> explicit reviewed changes`,
	"config": `FLOW: layer preferences without changing authority

  user -> canonical -> Source -> subdirectory -> effective config
                                  |
  execution-bearing values -> exact digest trust -> use`,
	"try": `FLOW: bounded exploration before formal research

  try run -> Attempt -> status/reconcile -> finish or abandon
                                              |
                                              +-> adopt -> Idea`,
	"idea": `FLOW: turn a direction into priced work

  idea add -> qualify/develop -> Plan -> queue insert
      |
      +-> remains reviewable until explicitly qualified`,
	"plan": `FLOW: price, order, and execute one research claim

  Idea -> Plan -> refresh -> Queue -> Experiment
                  |
                  +-> repin current evidence before dispatch`,
	"queue": `FLOW: transparent ordering under constrained pools

  pool add -> queue create -> queue insert/remove -> daemon frontier
                                |
                                +-> human pins and recorded agent advice`,
	"experiment": `FLOW: isolate execution and preserve evidence

  Plan -> workspace prepare -> Attempt/Run -> experiment close
                                             |
                                             +-> Findings and verdict`,
	"evaluation": `FLOW: apply a sealed comparable protocol

  evaluation spec create -> execute subject -> evaluation create
                                                 |
                                                 +-> immutable metrics/outcome`,
	"candidate": `FLOW: package supported evidence, not a guess

  closed supported Experiment + passing Evaluation + exact Attempt
                                  |
                                  +-> Candidate`,
	"release": `FLOW: assemble typed Candidates for one target

  Candidate slots -> optional combination evidence -> Release
                                                    |
                                                    +-> draft or validated`,
	"promotion": `FLOW: production remains a named human decision

  PromotionSpec + validated Release + holdout Evaluation
                            |
                            +-> Promotion -> champion -> manifest`,
	"daemon": `FLOW: reconcile first, then admit bounded work

  daemon status -> frontier -> tick/run -> local jobs
                      |             |
                 canonical only   provider observation`,
	"provider": `FLOW: explicit narrow operations keep upstream ownership

  doctor -> provider status/verify -> sanitized observation
                  |
                  +-> mutation only through a named confirmed command`,
}

type flowHelpAnnotation struct {
	flow  string
	guide string
}

var flowHelpAnnotations = map[string]flowHelpAnnotation{
	"exp":            {flow: "root", guide: "setup"},
	"exp init":       {flow: "setup", guide: "setup"},
	"exp workspace":  {flow: "setup", guide: "workspaces"},
	"exp source":     {flow: "source", guide: "workspaces"},
	"exp config":     {flow: "config", guide: "config"},
	"exp try":        {flow: "try", guide: "quick"},
	"exp idea":       {flow: "idea", guide: "research"},
	"exp plan":       {flow: "plan", guide: "research"},
	"exp queue":      {flow: "queue", guide: "research"},
	"exp experiment": {flow: "experiment", guide: "research"},
	"exp evaluation": {flow: "evaluation", guide: "research"},
	"exp candidate":  {flow: "candidate", guide: "promotion"},
	"exp release":    {flow: "release", guide: "promotion"},
	"exp promotion":  {flow: "promotion", guide: "promotion"},
	"exp daemon":     {flow: "daemon", guide: "research"},
	"exp provider":   {flow: "provider", guide: "config"},
}

func annotateFlowHelp(root *cobra.Command) error {
	if err := validateFlowHelpCatalog(root); err != nil {
		return err
	}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		annotation, found := flowHelpAnnotations[command.CommandPath()]
		if found {
			long := command.Long
			if long == "" {
				long = command.Short
			}
			long = strings.TrimRight(long, "\n") + "\n\n" + flowMaps[annotation.flow]
			if annotation.guide != "" {
				long += fmt.Sprintf("\n\nSee also: exp guide %s", annotation.guide)
			}
			command.Long = long
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
	return nil
}

func validateFlowHelpCatalog(root *cobra.Command) error {
	if root == nil {
		return fmt.Errorf("flow help command root is nil")
	}
	paths := map[string]struct{}{}
	var collect func(*cobra.Command)
	collect = func(command *cobra.Command) {
		paths[command.CommandPath()] = struct{}{}
		for _, child := range command.Commands() {
			collect(child)
		}
	}
	collect(root)
	for name, diagram := range flowMaps {
		if strings.TrimSpace(diagram) == "" {
			return fmt.Errorf("flow map %q is empty", name)
		}
		if !isASCII(diagram) {
			return fmt.Errorf("flow map %q is not ASCII", name)
		}
	}
	for path, annotation := range flowHelpAnnotations {
		if _, found := paths[path]; !found {
			return fmt.Errorf("flow help references missing command family %q", path)
		}
		if _, found := flowMaps[annotation.flow]; !found {
			return fmt.Errorf("flow help for %q references missing map %q", path, annotation.flow)
		}
		if annotation.guide != "" {
			if _, found := guideCatalog[annotation.guide]; !found {
				return fmt.Errorf("flow help for %q references missing guide %q", path, annotation.guide)
			}
		}
	}
	return nil
}

func isASCII(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, func(character rune) bool { return character > 0x7f }) < 0
}
