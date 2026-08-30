package record

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	ProjectFile        = "PROJECT.md"
	PolicyFile         = "POLICY.md"
	IdeasDir           = "ideas"
	ResourcePoolsDir   = "resource-pools"
	QueuesDir          = "queues"
	QueueAdviceDir     = "queue-advice"
	BattlesDir         = "battles"
	PlansDir           = "plans"
	EvaluationSpecsDir = "evaluation-specs"
	EvaluationsDir     = "evaluations"
	FindingsDir        = "findings"
	CandidatesDir      = "candidates"
	ReleasesDir        = "releases"
	PromotionSpecsDir  = "promotion-specs"
	PromotionsDir      = "promotions"
	DecisionsDir       = "decisions"
)

var generatedProjectionNames = map[string]struct{}{
	"README.md": {}, "ROADMAP.md": {}, "LEDGER.md": {}, "DECISIONS.md": {},
}

var (
	ideaNamePattern          = regexp.MustCompile(`^(idea_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	poolNamePattern          = regexp.MustCompile(`^(pool_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	queueNamePattern         = regexp.MustCompile(`^(queue_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	adviceNamePattern        = regexp.MustCompile(`^(advice_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	battleNamePattern        = regexp.MustCompile(`^(battle_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	planNamePattern          = regexp.MustCompile(`^(plan_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	evalSpecNamePattern      = regexp.MustCompile(`^(evalspec_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	evaluationNamePattern    = regexp.MustCompile(`^(eval_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	findingNamePattern       = regexp.MustCompile(`^(fnd_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	candidateNamePattern     = regexp.MustCompile(`^(cand_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	releaseNamePattern       = regexp.MustCompile(`^(rel_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	promotionSpecNamePattern = regexp.MustCompile(`^(promspec_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	promotionNamePattern     = regexp.MustCompile(`^(prom_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	decisionNamePattern      = regexp.MustCompile(`^(dec_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	runNamePattern           = regexp.MustCompile(`^(run_[0-9a-f-]{36})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	attemptNamePattern       = regexp.MustCompile(`^(att_[0-9a-f-]{36})\.md$`)
	experimentDirPattern     = regexp.MustCompile(`^e-([0-9a-f]{8,32})-([a-z0-9]+(?:-[a-z0-9]+)*)$`)
)

type flatLayout struct {
	kind    research.Kind
	pattern *regexp.Regexp
}

var flatLayouts = map[string]flatLayout{
	IdeasDir:           {kind: research.KindIdea, pattern: ideaNamePattern},
	ResourcePoolsDir:   {kind: research.KindResourcePool, pattern: poolNamePattern},
	QueuesDir:          {kind: research.KindQueue, pattern: queueNamePattern},
	QueueAdviceDir:     {kind: research.KindQueueAdvice, pattern: adviceNamePattern},
	BattlesDir:         {kind: research.KindBattle, pattern: battleNamePattern},
	PlansDir:           {kind: research.KindPlan, pattern: planNamePattern},
	EvaluationSpecsDir: {kind: research.KindEvaluationSpec, pattern: evalSpecNamePattern},
	EvaluationsDir:     {kind: research.KindEvaluation, pattern: evaluationNamePattern},
	FindingsDir:        {kind: research.KindFinding, pattern: findingNamePattern},
	CandidatesDir:      {kind: research.KindCandidate, pattern: candidateNamePattern},
	ReleasesDir:        {kind: research.KindRelease, pattern: releaseNamePattern},
	PromotionSpecsDir:  {kind: research.KindPromotionSpec, pattern: promotionSpecNamePattern},
	PromotionsDir:      {kind: research.KindPromotion, pattern: promotionNamePattern},
	DecisionsDir:       {kind: research.KindDecision, pattern: decisionNamePattern},
}

// CanonicalFlatDirs returns every reserved flat record directory.
func CanonicalFlatDirs() []string {
	directories := make([]string, 0, len(flatLayouts))
	for directory := range flatLayouts {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	return directories
}

// Location is the identity information encoded by one canonical relative path.
type Location struct {
	Relative         string
	Kind             research.Kind
	ID               research.ID
	Slug             string
	ExperimentDir    string
	ExperimentPrefix string
}

// ClassifyPath recognizes every canonical path. recognized is true for an
// invalid entry placed inside a reserved canonical location, so callers can
// report it rather than silently treating it as unrelated Markdown.
func ClassifyPath(relative string) (location Location, recognized bool, err error) {
	if relative == ProjectFile {
		return Location{Relative: relative, Kind: research.KindProject}, true, nil
	}
	if relative == PolicyFile {
		return Location{Relative: relative, Kind: research.KindPolicy}, true, nil
	}
	if _, generated := generatedProjectionNames[relative]; generated {
		return Location{}, false, nil
	}
	if relative == "" || strings.HasPrefix(relative, "/") || strings.Contains(relative, "\\") || path.Clean(relative) != relative {
		return Location{}, true, layoutError(relative, "path is not clean root-relative POSIX syntax")
	}
	parts := strings.Split(relative, "/")
	if len(parts) == 2 {
		if layout, found := flatLayouts[parts[0]]; found {
			match := layout.pattern.FindStringSubmatch(parts[1])
			if match == nil {
				return Location{}, true, layoutError(relative, "filename does not match the canonical %s layout", layout.kind)
			}
			id, parseErr := research.ParseIDForKind(match[1], layout.kind)
			if parseErr != nil {
				return Location{}, true, layoutError(relative, "%v", parseErr)
			}
			return Location{Relative: relative, Kind: layout.kind, ID: id, Slug: match[2]}, true, nil
		}
	}
	if len(parts) > 0 {
		_, reserved := flatLayouts[parts[0]]
		if reserved {
			return Location{}, true, layoutError(relative, "nested path is not allowed in the reserved %s tree", parts[0])
		}
	}
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "e-") {
		return Location{}, false, nil
	}
	directory := experimentDirPattern.FindStringSubmatch(parts[0])
	if directory == nil {
		return Location{}, true, layoutError(relative, "experiment directory does not match e-<short-prefix>-<slug>")
	}
	base := Location{
		Relative:         relative,
		Slug:             directory[2],
		ExperimentDir:    parts[0],
		ExperimentPrefix: directory[1],
	}
	switch {
	case len(parts) == 2 && parts[1] == "REPORT.md":
		base.Kind = research.KindExperiment
		return base, true, nil
	case len(parts) == 3 && parts[1] == "runs":
		match := runNamePattern.FindStringSubmatch(parts[2])
		if match == nil {
			return Location{}, true, layoutError(relative, "run filename does not match the canonical layout")
		}
		id, parseErr := research.ParseIDForKind(match[1], research.KindRun)
		if parseErr != nil {
			return Location{}, true, layoutError(relative, "%v", parseErr)
		}
		base.Kind, base.ID, base.Slug = research.KindRun, id, match[2]
		return base, true, nil
	case len(parts) == 3 && parts[1] == "attempts":
		match := attemptNamePattern.FindStringSubmatch(parts[2])
		if match == nil {
			return Location{}, true, layoutError(relative, "attempt filename does not match the canonical layout")
		}
		id, parseErr := research.ParseIDForKind(match[1], research.KindAttempt)
		if parseErr != nil {
			return Location{}, true, layoutError(relative, "%v", parseErr)
		}
		base.Kind, base.ID = research.KindAttempt, id
		return base, true, nil
	default:
		return Location{}, true, layoutError(relative, "path is not a canonical Experiment, Run, or Attempt record")
	}
}

func layoutError(relative, format string, args ...any) error {
	return &Error{Code: "record.invalid_path", Message: fmt.Sprintf("%s: %s", relative, fmt.Sprintf(format, args...)), Err: ErrInvalidPath}
}

// ValidateDocumentPath checks the kind and immutable identity encoded by location.
func ValidateDocumentPath(location Location, document *Document) error {
	if document == nil || document.Record == nil {
		return layoutError(location.Relative, "document is nil")
	}
	if location.Kind != document.Kind() {
		return layoutError(location.Relative, "path is for %s but front matter is %s", location.Kind, document.Kind())
	}
	if location.Kind == research.KindProject || location.Kind == research.KindPolicy {
		return nil
	}
	id, ok := document.ID()
	if !ok {
		return layoutError(location.Relative, "front matter has no typed ID")
	}
	if location.Kind == research.KindExperiment {
		if !strings.HasPrefix(id.UUIDHex(), location.ExperimentPrefix) {
			return layoutError(location.Relative, "experiment directory prefix %s does not match ID %s", location.ExperimentPrefix, id)
		}
		return nil
	}
	if id != location.ID {
		return layoutError(location.Relative, "path ID %s does not match front matter ID %s", location.ID, id)
	}
	return nil
}

// Slug generates a lower-case ASCII navigation slug. A title with no ASCII
// letters or digits receives the supplied stable fallback.
func Slug(title, fallback string) string {
	var builder strings.Builder
	separator := false
	for _, character := range strings.ToLower(title) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(character)
			separator = false
		case builder.Len() > 0:
			separator = true
		}
		if builder.Len() >= 80 {
			break
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > 80 {
		result = strings.TrimRight(result[:80], "-")
	}
	if result == "" {
		result = fallback
	}
	if !validLayoutSlug(result) {
		return "record"
	}
	return result
}

func validLayoutSlug(value string) bool {
	return regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(value)
}

// PathForNew allocates the canonical path for a new record against inventory.
func PathForNew(value research.Record, inventory *Inventory) (string, error) {
	if value == nil {
		return "", layoutError("", "record is nil")
	}
	if value.GetKind() == research.KindProject {
		return ProjectFile, nil
	}
	if value.GetKind() == research.KindPolicy {
		return PolicyFile, nil
	}
	id, ok := value.GetID()
	if !ok {
		return "", layoutError("", "%s record has no ID", value.GetKind())
	}
	common := value.GetCommon()
	switch record := value.(type) {
	case *research.Idea:
		return path.Join(IdeasDir, id.String()+"-"+Slug(common.Title, "idea")+".md"), nil
	case *research.ResourcePool:
		return path.Join(ResourcePoolsDir, id.String()+"-"+Slug(common.Title, "pool")+".md"), nil
	case *research.Queue:
		return path.Join(QueuesDir, id.String()+"-"+Slug(common.Title, "queue")+".md"), nil
	case *research.QueueAdvice:
		return path.Join(QueueAdviceDir, id.String()+"-"+Slug(common.Title, "advice")+".md"), nil
	case *research.Battle:
		return path.Join(BattlesDir, id.String()+"-"+Slug(common.Title, "battle")+".md"), nil
	case *research.Plan:
		return path.Join(PlansDir, id.String()+"-"+Slug(common.Title, "plan")+".md"), nil
	case *research.EvaluationSpec:
		return path.Join(EvaluationSpecsDir, id.String()+"-"+Slug(common.Title, "evaluation-spec")+".md"), nil
	case *research.Evaluation:
		return path.Join(EvaluationsDir, id.String()+"-"+Slug(common.Title, "evaluation")+".md"), nil
	case *research.Finding:
		return path.Join(FindingsDir, id.String()+"-"+Slug(common.Title, "finding")+".md"), nil
	case *research.Candidate:
		return path.Join(CandidatesDir, id.String()+"-"+Slug(common.Title, "candidate")+".md"), nil
	case *research.Release:
		return path.Join(ReleasesDir, id.String()+"-"+Slug(common.Title, "release")+".md"), nil
	case *research.PromotionSpec:
		return path.Join(PromotionSpecsDir, id.String()+"-"+Slug(common.Title, "promotion-spec")+".md"), nil
	case *research.Promotion:
		return path.Join(PromotionsDir, id.String()+"-"+Slug(common.Title, "promotion")+".md"), nil
	case *research.Decision:
		return path.Join(DecisionsDir, id.String()+"-"+Slug(common.Title, "decision")+".md"), nil
	case *research.Experiment:
		var candidates []research.ReferenceCandidate
		if inventory != nil {
			for _, document := range inventory.OfKind(research.KindExperiment) {
				candidateID, _ := document.ID()
				candidates = append(candidates, research.ReferenceCandidate{ID: candidateID})
			}
		}
		code, err := research.DisplayCode(id, candidates)
		if err != nil {
			return "", err
		}
		prefix := strings.ToLower(strings.TrimPrefix(code, "E-"))
		return path.Join("e-"+prefix+"-"+Slug(common.Title, "experiment"), "REPORT.md"), nil
	case *research.Run:
		if inventory == nil {
			return "", layoutError("", "Run path allocation requires an inventory")
		}
		experiment, err := inventory.ByID(record.Experiment)
		if err != nil {
			return "", err
		}
		location, found := inventory.Location(experiment)
		if !found || location.ExperimentDir == "" {
			return "", layoutError(experiment.Path, "owning Experiment has no canonical directory")
		}
		return path.Join(location.ExperimentDir, "runs", id.String()+"-"+Slug(common.Title, "run")+".md"), nil
	case *research.Attempt:
		if inventory == nil {
			return "", layoutError("", "Attempt path allocation requires an inventory")
		}
		run, err := inventory.ByID(record.Run)
		if err != nil {
			return "", err
		}
		location, found := inventory.Location(run)
		if !found || location.ExperimentDir == "" {
			return "", layoutError(run.Path, "owning Run has no canonical Experiment directory")
		}
		return path.Join(location.ExperimentDir, "attempts", id.String()+".md"), nil
	default:
		return "", layoutError("", "unsupported record type %T", value)
	}
}
