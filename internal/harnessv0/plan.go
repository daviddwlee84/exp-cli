package harnessv0

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/projection"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const maxPlanBytes int64 = 128 << 20

type BuildRequest struct {
	RepositoryRoot string
	SourceRoot     string
	GeneratedAt    time.Time
	Resolutions    ResolutionSet
}

// BuildPlan reads harness-v0 bytes without mutation and emits a complete,
// fingerprinted migration plan.
func BuildPlan(ctx context.Context, request BuildRequest) (*Plan, error) {
	if err := pathx.ValidateRelativePOSIX(request.SourceRoot, false); err != nil {
		return nil, fmt.Errorf("invalid migration source root: %w", err)
	}
	sourcePath, err := pathx.ResolveUnderNoSymlinks(request.RepositoryRoot, request.SourceRoot, false)
	if err != nil {
		return nil, err
	}
	tree, err := readTree(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	for _, file := range tree.Files {
		if file.Path == record.ProjectFile {
			return nil, fmt.Errorf("source already contains PROJECT.md; harness-v0 migration only accepts an unversioned root")
		}
	}
	parsed, err := parseTree(tree)
	if err != nil {
		return nil, err
	}
	parsed.Diagnostics = append(parsed.Diagnostics, legacyGuidanceDiagnostics(request.RepositoryRoot)...)
	generatedAt := request.GeneratedAt.UTC()
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	if request.Resolutions.SchemaVersion != "" && request.Resolutions.SchemaVersion != ResolutionSchema {
		return nil, fmt.Errorf("unsupported resolution schema %q", request.Resolutions.SchemaVersion)
	}
	resolutions, err := normalizeResolutions(request.Resolutions.Resolutions)
	if err != nil {
		return nil, err
	}
	projectID, err := research.ImportedProjectID(tree.Fingerprint)
	if err != nil {
		return nil, err
	}
	sourceFiles := make([]SourceFile, len(tree.Files))
	var unknown []SpanReference
	for index := range tree.Files {
		sourceFiles[index] = tree.Files[index].SourceFile
		for _, span := range sourceFiles[index].Spans {
			if span.Kind == "unknown" {
				unknown = append(unknown, SpanReference{Path: sourceFiles[index].Path, StartByte: span.StartByte, EndByte: span.EndByte, SHA256: span.SHA256})
			}
		}
	}
	archivePath := "legacy/harness-v0/" + strings.TrimPrefix(tree.Fingerprint, "sha256:")
	manifestValue := manifest{
		Schema: ManifestSchema, ReaderVersion: research.HarnessV0Reader,
		TreeFingerprint: tree.Fingerprint, SourceRoot: request.SourceRoot,
		Files: sourceFiles, Resolutions: resolutions,
	}
	manifestBytes, err := encodeManifest(manifestValue)
	if err != nil {
		return nil, err
	}

	builder := &planBuilder{
		tree: tree, parsed: parsed, generatedAt: generatedAt, projectID: projectID,
		fingerprint: tree.Fingerprint, archivePath: archivePath,
		manifestHash: hashBytes(manifestBytes), resolutions: resolutionMap(resolutions),
		diagnostics: append([]Diagnostic(nil), parsed.Diagnostics...),
		reportIDs:   map[string]research.ID{}, findingIDs: map[string]research.ID{},
	}
	builder.indexAliases()
	builder.buildProject(projectName(tree), manifestBytes)
	builder.buildExperiments()
	builder.buildFindings()
	builder.buildPlans()
	builder.finishMappings()
	candidates, projections, candidateErr := builder.selectedCandidateFiles()
	if candidateErr != nil {
		builder.diagnostics = append(builder.diagnostics, Diagnostic{State: "error", Code: "candidate.inventory", Message: candidateErr.Error()})
		builder.applicable = false
	}
	candidates = append(candidates, projections...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	sort.Slice(builder.mappings, func(i, j int) bool { return builder.mappings[i].Key < builder.mappings[j].Key })
	sort.Slice(builder.diagnostics, func(i, j int) bool {
		if builder.diagnostics[i].Path != builder.diagnostics[j].Path {
			return builder.diagnostics[i].Path < builder.diagnostics[j].Path
		}
		if builder.diagnostics[i].StartByte != builder.diagnostics[j].StartByte {
			return builder.diagnostics[i].StartByte < builder.diagnostics[j].StartByte
		}
		return builder.diagnostics[i].Code < builder.diagnostics[j].Code
	})
	plan := &Plan{
		SchemaVersion: PlanSchema, ReaderVersion: research.HarnessV0Reader,
		TargetSchemaVersion: string(research.SchemaProject), GeneratedAt: generatedAt,
		SourceRoot: request.SourceRoot, TreeFingerprint: tree.Fingerprint,
		ProjectID: projectID.String(), Applicable: builder.applicable && candidateErr == nil,
		SourceFiles: sourceFiles, Mappings: builder.mappings, UnknownSpans: unknown,
		Diagnostics: builder.diagnostics, Resolutions: resolutions,
		Archive:        Archive{Path: archivePath, ManifestSHA256: hashBytes(manifestBytes), ManifestContentBase64: base64.StdEncoding.EncodeToString(manifestBytes)},
		CandidateFiles: candidates,
	}
	plan.UnifiedDiff = renderUnifiedDiff(candidates)
	plan.ContentHash, err = ComputePlanHash(plan)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

type planBuilder struct {
	tree         *sourceTree
	parsed       *parsedTree
	generatedAt  time.Time
	projectID    research.UUID
	fingerprint  string
	archivePath  string
	manifestHash string
	resolutions  map[string]Resolution
	diagnostics  []Diagnostic
	mappings     []Mapping
	documents    map[string]*record.Document
	reportIDs    map[string]research.ID
	findingIDs   map[string]research.ID
	applicable   bool
}

func (builder *planBuilder) indexAliases() {
	reportCounts := map[string]int{}
	for _, report := range builder.parsed.Reports {
		reportCounts[report.Alias]++
	}
	for alias, count := range reportCounts {
		if count != 1 {
			builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: "experiment:" + alias, State: "needs_review", Code: "alias.duplicate", Message: fmt.Sprintf("experiment alias %s occurs %d times", alias, count)})
			continue
		}
		id, err := research.ImportedRecordID(builder.projectID, research.KindExperiment, alias)
		if err == nil {
			builder.reportIDs[alias] = id
		}
	}
	findingCounts := map[string]int{}
	for _, finding := range builder.parsed.Findings {
		findingCounts[finding.Alias]++
	}
	for alias, count := range findingCounts {
		if count != 1 {
			builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: "finding:" + alias, State: "needs_review", Code: "alias.duplicate", Message: fmt.Sprintf("finding alias %s occurs %d times", alias, count)})
			continue
		}
		id, err := research.ImportedRecordID(builder.projectID, research.KindFinding, alias)
		if err == nil {
			builder.findingIDs[alias] = id
		}
	}
}

func (builder *planBuilder) buildProject(name string, manifest []byte) {
	extension := research.Extensions{research.MigrationExtension: {
		"fingerprint": builder.fingerprint, "namespace": research.HarnessV0Namespace,
		"reader_version": research.HarnessV0Reader, "archive_path": builder.archivePath,
		"manifest_sha256": hashBytes(manifest),
	}}
	document := &record.Document{Path: record.ProjectFile, Body: "\n# " + name + "\n", Record: &research.Project{
		Schema: research.SchemaProject, ProjectID: builder.projectID, Name: name,
		CreatedAt: builder.generatedAt, ExperimentsRoot: ".", Extensions: extension,
	}}
	builder.documents = map[string]*record.Document{record.ProjectFile: document}
}

func (builder *planBuilder) buildExperiments() {
	for _, legacy := range builder.parsed.Reports {
		key := "experiment:" + legacy.Alias
		id, unique := builder.reportIDs[legacy.Alias]
		mapping := Mapping{Key: key, Kind: research.KindExperiment.String(), SourcePath: legacy.File.Path, StartByte: legacy.Start, EndByte: legacy.End, StableSourceKey: legacy.Alias, Status: "candidate", ReviewKeys: []string{}}
		if !unique {
			builder.needReview(&mapping, "alias.duplicate", "duplicate experiment alias prevents deterministic mapping")
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		mapping.ID = id.String()
		status := legacy.Fields["status"]
		lifecycle := research.LifecyclePlanned
		var closure research.ExperimentClosure
		var closureDetail *research.ClosureDetail
		switch status {
		case "planned":
		case "running":
			lifecycle = research.LifecycleActive
		case "superseded":
			replacementAlias := ""
			if refs := experimentRefPattern.FindAllString(legacy.Fields["conclusion"], -1); len(refs) == 1 {
				replacementAlias = refs[0]
			}
			replacement, found := builder.reportIDs[replacementAlias]
			if !found {
				builder.needReview(&mapping, "experiment.superseded_target", "superseded report has no unique explicit replacement")
			} else {
				lifecycle, closure = research.LifecycleClosed, research.ClosureSuperseded
				closureDetail = &research.ClosureDetail{Reason: legacy.Fields["conclusion"], SupersededBy: replacement}
			}
		case "concluded-success", "concluded-negative", "inconclusive":
			builder.needReview(&mapping, "experiment.conclusion_evidence", "closed legacy report has no v1 Run evidence disposition; archive-only is required until evidence is reconstructed")
		default:
			builder.needReview(&mapping, "experiment.status", "legacy report status is missing or unsupported")
		}
		axis := strings.TrimSpace(legacy.Fields["axis"])
		if multiFactor(axis) {
			builder.needReview(&mapping, "experiment.primary_factor", "legacy axis is multi-factor or ambiguous; migration will not select a primary factor")
		}
		hypothesis := extractLabel(legacy.Body, "Hypothesis")
		success := extractLabel(legacy.Body, "Success criteria")
		decisionRule := extractLabel(legacy.Body, "Decision rule")
		required := map[string]string{
			"title": legacy.Fields["title"], "question": legacy.Fields["question"], "axis": axis,
			"baseline": legacy.Fields["baseline"], "spec": legacy.Fields["spec"],
			"hypothesis": hypothesis, "success_criteria": success, "decision_rule": decisionRule,
		}
		missing := false
		for field, value := range required {
			if strings.TrimSpace(value) == "" || strings.Contains(value, "<") {
				builder.needReview(&mapping, "experiment.required_meaning", "legacy report lacks reviewed "+field)
				missing = true
			}
		}
		if status == "concluded-success" || status == "concluded-negative" || status == "inconclusive" || missing || multiFactor(axis) {
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		created := dateOr(legacy.Fields["started"], builder.generatedAt)
		updated := created
		if concluded := parseDate(legacy.Fields["concluded"]); !concluded.IsZero() {
			updated = concluded
		}
		tags, tagsChanged := safeTags(legacy.Lists["tags"])
		if tagsChanged {
			builder.needReview(&mapping, "report.tags", "one or more legacy tags are not valid v1 tags and remain only in the archive")
		}
		document := &record.Document{Body: legacy.Body, Record: &research.Experiment{
			Common:    research.Common{Schema: research.SchemaExperiment, ID: id, Title: legacy.Fields["title"], CreatedAt: created, UpdatedAt: updated, LegacyAliases: []string{legacy.Alias}, Tags: tags},
			Lifecycle: lifecycle, Closure: closure, ClosureDetail: closureDetail,
			Design:     research.Design{Question: legacy.Fields["question"], Hypothesis: hypothesis, Kind: research.ExperimentSingleFactor, PrimaryFactor: axis, SecondaryFactors: []string{}, Baseline: legacy.Fields["baseline"], ComparabilitySpec: legacy.Fields["spec"], SuccessCriteria: []string{success}, DecisionRule: decisionRule},
			Extensions: migrationExtension(builder, legacy.File, legacy.Start, legacy.End, legacy.Alias),
		}}
		document.Path = experimentPath(id, legacy.Fields["slug"], legacy.Fields["title"])
		builder.addCandidate(&mapping, document)
		builder.mappings = append(builder.mappings, mapping)
	}
}

func (builder *planBuilder) buildFindings() {
	documents := map[string]*research.Finding{}
	mappingIndexes := map[string]int{}
	for _, legacy := range builder.parsed.Findings {
		key := "finding:" + legacy.Alias
		id, unique := builder.findingIDs[legacy.Alias]
		mapping := Mapping{Key: key, Kind: research.KindFinding.String(), SourcePath: legacy.File.Path, StartByte: legacy.Start, EndByte: legacy.End, StableSourceKey: legacy.Alias, Status: "candidate", ReviewKeys: []string{}}
		if !unique {
			builder.needReview(&mapping, "alias.duplicate", "duplicate finding alias prevents deterministic mapping")
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		mapping.ID = id.String()
		experimentID, evidenceFound := builder.reportIDs[legacy.Experiment]
		if !evidenceFound || legacy.Experiment == "ext" {
			builder.needReview(&mapping, "finding.evidence", "finding has no unique migrated Experiment evidence")
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		if _, exists := builder.documentsByID(experimentID); !exists {
			builder.needReview(&mapping, "finding.evidence", "finding evidence report is archive-only or not migratable")
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		statement := strings.TrimSpace(legacy.Statement)
		document := &record.Document{Body: legacy.Raw, Record: &research.Finding{
			Common:    research.Common{Schema: research.SchemaFinding, ID: id, Title: findingTitle(legacy.Alias, statement), CreatedAt: dateOr(legacy.Date, builder.generatedAt), UpdatedAt: dateOr(legacy.Date, builder.generatedAt), LegacyAliases: []string{legacy.Alias}},
			Statement: statement, Scope: "Imported harness-v0 finding; exact scope is retained in the archived source span.",
			Evidence:   []research.FindingEvidence{{Kind: research.FindingEvidenceExperiment, Ref: experimentID, Detail: "Coarse migrated Experiment evidence from " + legacy.Experiment}},
			Extensions: migrationExtension(builder, legacy.File, legacy.Start, legacy.End, legacy.Alias),
		}}
		document.Path = flatPath(record.FindingsDir, id, statement)
		builder.addCandidate(&mapping, document)
		documents[legacy.Alias] = document.Record.(*research.Finding)
		builder.mappings = append(builder.mappings, mapping)
		mappingIndexes[legacy.Alias] = len(builder.mappings) - 1
	}
	for _, legacy := range builder.parsed.Findings {
		if legacy.WeakenedBy != "" {
			if source := documents[legacy.WeakenedBy]; source != nil {
				source.Weakens = append(source.Weakens, builder.findingIDs[legacy.Alias])
			} else if index, ok := mappingIndexes[legacy.Alias]; ok {
				builder.needReview(&builder.mappings[index], "finding.weakened_link", "weakened marker does not resolve to a unique migrated Finding")
			}
		}
		if legacy.OverturnedBy != "" {
			if source := documents[legacy.OverturnedBy]; source != nil {
				source.Overturns = append(source.Overturns, builder.findingIDs[legacy.Alias])
			} else if index, ok := mappingIndexes[legacy.Alias]; ok {
				builder.needReview(&builder.mappings[index], "finding.overturn_link", "overturn marker does not resolve to a unique migrated Finding")
			}
		}
	}
	// Edge ownership changes record bytes, so refresh the mapping payloads.
	for alias, document := range documents {
		index := mappingIndexes[alias]
		builder.refreshCandidate(&builder.mappings[index], builder.documentForID(document.ID))
	}
}

func (builder *planBuilder) buildPlans() {
	for _, legacy := range builder.parsed.Plans {
		stable := fmt.Sprintf("ROADMAP.md:%d:%d:%s", legacy.Start, legacy.End, strings.TrimPrefix(hashBytes(legacy.File.Data[legacy.Start:legacy.End]), "sha256:"))
		key := "plan:" + stable
		id, err := research.ImportedRecordID(builder.projectID, research.KindPlan, stable)
		mapping := Mapping{Key: key, Kind: research.KindPlan.String(), SourcePath: legacy.File.Path, StartByte: legacy.Start, EndByte: legacy.End, StableSourceKey: stable, Status: "candidate", ReviewKeys: []string{}}
		if err != nil {
			builder.needReview(&mapping, "plan.identity", err.Error())
			builder.mappings = append(builder.mappings, mapping)
			continue
		}
		mapping.ID = id.String()
		priority := research.Priority(legacy.Lane)
		if priority != research.PriorityP1 && priority != research.PriorityP2 && priority != research.PriorityP3 && priority != research.PriorityUnknown {
			builder.needReview(&mapping, "plan.priority", "roadmap item has no recognized priority lane")
			priority = research.PriorityUnknown
		}
		payoff := legacy.Payoff
		if payoff == "" {
			payoff = "Legacy payoff is not explicit; consult the archived source span."
		}
		builder.needReview(&mapping, "plan.payoff_untyped", "legacy payoff has no separable v1 metric and unit; the candidate retains it as legacy text")
		state := research.PlanQueued
		var resulting research.ID
		created := builder.generatedAt
		if legacy.Done {
			state = research.PlanCompleted
			created = dateOr(legacy.DoneDate, builder.generatedAt)
			resulting = builder.reportIDs[legacy.Experiment]
			if resulting.IsZero() {
				builder.needReview(&mapping, "plan.result", "completed roadmap item does not resolve to a unique Experiment")
				builder.mappings = append(builder.mappings, mapping)
				continue
			}
		}
		var assumptions []research.ID
		for _, dependency := range legacy.DependsOn {
			if strings.HasPrefix(dependency, "F-") {
				if id := builder.findingIDs[dependency]; !id.IsZero() {
					assumptions = append(assumptions, id)
				} else {
					builder.needReview(&mapping, "plan.dependency", "roadmap finding dependency does not resolve uniquely")
				}
			} else {
				builder.needReview(&mapping, "plan.experiment_dependency", "experiment dependency has no lossless v1 Plan field and remains in the archive")
			}
		}
		tags, changed := safeTags([]string{legacy.Category})
		if changed && legacy.Category != "" {
			builder.needReview(&mapping, "plan.category", "legacy category is not a valid v1 tag and remains in the archive")
		}
		document := &record.Document{Body: legacy.Text, Record: &research.Plan{
			Common:   research.Common{Schema: research.SchemaPlan, ID: id, Title: legacy.Title, CreatedAt: created, UpdatedAt: created, Tags: tags},
			Priority: priority, Effort: research.Effort(legacy.Effort), State: state, Assumptions: assumptions, ResultingExperiment: resulting,
			ExpectedPayoff: research.ExpectedPayoff{Summary: payoff, Metric: "legacy-payoff", Unit: "legacy-text"},
			Extensions:     migrationExtension(builder, legacy.File, legacy.Start, legacy.End, stable),
		}}
		document.Path = flatPath(record.PlansDir, id, legacy.Title)
		builder.addCandidate(&mapping, document)
		builder.mappings = append(builder.mappings, mapping)
	}
}

func (builder *planBuilder) needReview(mapping *Mapping, code, message string) {
	if mapping.Status != "needs_review" {
		mapping.Status = "needs_review"
		mapping.ReviewKeys = []string{mapping.Key}
	}
	builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: mapping.Key, State: "needs_review", Code: code, Message: message, Path: mapping.SourcePath, StartByte: mapping.StartByte, EndByte: mapping.EndByte})
}

func (builder *planBuilder) addCandidate(mapping *Mapping, document *record.Document) {
	content, err := encodeMigrationDocument(document)
	if err != nil {
		builder.needReview(mapping, "candidate.invalid", err.Error())
		return
	}
	mapping.Destination = document.Path
	mapping.CandidateSHA256 = hashBytes(content)
	mapping.CandidateContent = base64.StdEncoding.EncodeToString(content)
	builder.documents[document.Path] = document
}

func (builder *planBuilder) refreshCandidate(mapping *Mapping, document *record.Document) {
	if document == nil || mapping.CandidateContent == "" {
		return
	}
	content, err := encodeMigrationDocument(document)
	if err != nil {
		builder.needReview(mapping, "candidate.invalid", err.Error())
		mapping.CandidateContent, mapping.CandidateSHA256, mapping.Destination = "", "", ""
		return
	}
	mapping.CandidateSHA256 = hashBytes(content)
	mapping.CandidateContent = base64.StdEncoding.EncodeToString(content)
}

func (builder *planBuilder) finishMappings() {
	builder.applicable = true
	reviewKeys := map[string]bool{}
	for _, diagnostic := range builder.diagnostics {
		if diagnostic.State == "needs_review" && diagnostic.Key != "" {
			reviewKeys[diagnostic.Key] = true
		}
	}
	for key := range reviewKeys {
		resolution, found := builder.resolutions[key]
		if !found {
			builder.applicable = false
			continue
		}
		if resolution.Action != "archive_only" && resolution.Action != "migrate" {
			builder.applicable = false
			builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: key, State: "error", Code: "resolution.action", Message: "resolution action must be migrate or archive_only"})
		}
	}
	for index := range builder.mappings {
		mapping := &builder.mappings[index]
		if mapping.Status != "needs_review" {
			mapping.Status = "selected"
			continue
		}
		resolution, found := builder.resolutions[mapping.Key]
		if !found {
			continue
		}
		switch resolution.Action {
		case "archive_only":
			mapping.Status = "archive_only"
		case "migrate":
			if mapping.CandidateContent == "" {
				builder.applicable = false
				builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: mapping.Key, State: "error", Code: "resolution.not_migratable", Message: "this ambiguous item has no valid canonical candidate; choose archive_only"})
			} else {
				mapping.Status = "selected"
			}
		}
	}
	for _, resolution := range builder.resolutions {
		if !reviewKeys[resolution.Key] {
			builder.applicable = false
			builder.diagnostics = append(builder.diagnostics, Diagnostic{Key: resolution.Key, State: "error", Code: "resolution.unknown", Message: "resolution key does not match a needs_review item"})
		}
	}
}

func (builder *planBuilder) selectedCandidateFiles() ([]CandidateFile, []CandidateFile, error) {
	selectedPaths := map[string]bool{record.ProjectFile: true}
	for _, mapping := range builder.mappings {
		if mapping.Status == "selected" && mapping.Destination != "" {
			selectedPaths[mapping.Destination] = true
		}
	}
	documents := make([]*record.Document, 0, len(selectedPaths))
	files := make([]CandidateFile, 0, len(selectedPaths))
	for path := range selectedPaths {
		document := builder.documents[path]
		if document == nil {
			continue
		}
		content, err := encodeMigrationDocument(document)
		if err != nil {
			return nil, nil, err
		}
		documents = append(documents, document)
		files = append(files, CandidateFile{Path: path, SHA256: hashBytes(content), ContentBase64: base64.StdEncoding.EncodeToString(content)})
	}
	inventory := record.InventoryFromMigratedDocuments("", documents)
	if !inventory.Valid() {
		return files, nil, inventory.Error()
	}
	generated, err := projection.Build(inventory)
	if err != nil {
		return files, nil, err
	}
	projectionFiles := make([]CandidateFile, 0, len(generated))
	for _, file := range generated {
		projectionFiles = append(projectionFiles, CandidateFile{Path: file.Path, SHA256: hashBytes(file.Content), ContentBase64: base64.StdEncoding.EncodeToString(file.Content), Generated: true})
	}
	return files, projectionFiles, nil
}

func (builder *planBuilder) documentsByID(id research.ID) (*record.Document, bool) {
	for _, document := range builder.documents {
		candidate, ok := document.ID()
		if ok && candidate == id {
			return document, true
		}
	}
	return nil, false
}

func (builder *planBuilder) documentForID(id research.ID) *record.Document {
	document, _ := builder.documentsByID(id)
	return document
}

func migrationExtension(builder *planBuilder, file *sourceData, start, end int64, stable string) research.Extensions {
	return research.Extensions{research.MigrationExtension: {
		"fingerprint": builder.fingerprint, "source_path": file.Path,
		"source_sha256": file.SHA256, "start_byte": start, "end_byte": end,
		"span_sha256": hashBytes(file.Data[start:end]), "stable_source_key": stable,
	}}
}

func experimentPath(id research.ID, legacySlug, title string) string {
	slug := record.Slug(legacySlug, "migrated-experiment")
	if legacySlug == "" {
		slug = record.Slug(title, "migrated-experiment")
	}
	return fmt.Sprintf("e-%s-%s/REPORT.md", id.UUIDHex()[:8], slug)
}

func flatPath(directory string, id research.ID, title string) string {
	return directory + "/" + id.String() + "-" + record.Slug(title, "migrated") + ".md"
}

func findingTitle(alias, statement string) string {
	statement = strings.Join(strings.Fields(regexp.MustCompile(`\[[^]]*\]\([^)]*\)|[*_~]+`).ReplaceAllString(statement, "")), " ")
	if len(statement) > 72 {
		statement = statement[:72]
	}
	return strings.TrimSpace(alias + " " + statement)
}

func extractLabel(body, label string) string {
	pattern := regexp.MustCompile(`(?mi)^-\s*\*\*` + regexp.QuoteMeta(label) + `\*\*:\s*(.+)$`)
	match := pattern.FindStringSubmatch(body)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func multiFactor(axis string) bool {
	lower := strings.ToLower(axis)
	return strings.ContainsAny(axis, ",+") || strings.Contains(lower, " and ") || strings.Contains(lower, " vs ") && strings.Contains(axis, "/")
}

func safeTags(values []string) ([]string, bool) {
	var output []string
	changed := false
	valid := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !valid.MatchString(value) || seen[value] {
			changed = true
			continue
		}
		seen[value] = true
		output = append(output, value)
	}
	sort.Strings(output)
	return output, changed
}

func parseDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func dateOr(value string, fallback time.Time) time.Time {
	if parsed := parseDate(value); !parsed.IsZero() {
		return parsed
	}
	return fallback.UTC()
}

func projectName(tree *sourceTree) string {
	for _, file := range tree.Files {
		if file.Path != "README.md" || !utf8Valid(file.Data) {
			continue
		}
		for _, line := range splitLines(file.Data) {
			if strings.HasPrefix(line.text, "# ") {
				name := strings.TrimSpace(strings.TrimPrefix(line.text, "# "))
				if name != "" {
					return name
				}
			}
		}
	}
	return "Migrated harness-v0 project"
}

func legacyGuidanceDiagnostics(repositoryRoot string) []Diagnostic {
	var diagnostics []Diagnostic
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		candidate := filepath.Join(repositoryRoot, name)
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err != nil || !bytes.Contains(bytes.ToLower(data), []byte("experiment-knowledge-harness")) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{State: "info", Code: "source.skill_guidance", Message: "project-local legacy skill guidance is advisory only and is not canonical migration input", Path: name})
	}
	return diagnostics
}

func utf8Valid(data []byte) bool { return strings.ToValidUTF8(string(data), "") == string(data) }

func normalizeResolutions(input []Resolution) ([]Resolution, error) {
	output := append([]Resolution(nil), input...)
	sort.Slice(output, func(i, j int) bool { return output[i].Key < output[j].Key })
	for index := range output {
		output[index].Key = strings.TrimSpace(output[index].Key)
		output[index].Action = strings.TrimSpace(output[index].Action)
		if output[index].Key == "" || (output[index].Action != "migrate" && output[index].Action != "archive_only") {
			return nil, fmt.Errorf("resolution %d has invalid key or action", index)
		}
		if index > 0 && output[index-1].Key == output[index].Key {
			return nil, fmt.Errorf("duplicate resolution key %s", output[index].Key)
		}
	}
	return output, nil
}

func resolutionMap(values []Resolution) map[string]Resolution {
	output := make(map[string]Resolution, len(values))
	for _, value := range values {
		output[value.Key] = value
	}
	return output
}

func encodeManifest(value manifest) ([]byte, error) {
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(value); err != nil {
		return nil, fmt.Errorf("encode migration archive manifest: %w", err)
	}
	return output.Bytes(), nil
}

func renderUnifiedDiff(files []CandidateFile) string {
	var output strings.Builder
	for _, file := range files {
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			continue
		}
		fmt.Fprintf(&output, "--- /dev/null\n+++ b/%s\n", file.Path)
		for _, line := range strings.SplitAfter(string(content), "\n") {
			if line != "" {
				output.WriteByte('+')
				output.WriteString(line)
			}
		}
	}
	return output.String()
}

// ComputePlanHash hashes the semantic JSON plan with content_hash blank.
func ComputePlanHash(plan *Plan) (string, error) {
	if plan == nil {
		return "", errors.New("migration plan is nil")
	}
	copy := *plan
	copy.ContentHash = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func EncodePlan(plan *Plan) ([]byte, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodePlan(reader io.Reader) (*Plan, error) {
	limited := io.LimitReader(reader, maxPlanBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxPlanBytes {
		return nil, fmt.Errorf("migration plan exceeds %d bytes", maxPlanBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan Plan
	if err := decoder.Decode(&plan); err != nil {
		return nil, fmt.Errorf("decode migration plan: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := ValidatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func DecodeResolutions(reader io.Reader) (ResolutionSet, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maxPlanBytes+1))
	decoder.DisallowUnknownFields()
	var values ResolutionSet
	if err := decoder.Decode(&values); err != nil {
		return values, fmt.Errorf("decode migration resolutions: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return values, err
	}
	if values.SchemaVersion != ResolutionSchema {
		return values, fmt.Errorf("unsupported resolution schema %q", values.SchemaVersion)
	}
	normalized, err := normalizeResolutions(values.Resolutions)
	values.Resolutions = normalized
	return values, err
}

func ValidatePlan(plan *Plan) error {
	if plan == nil || plan.SchemaVersion != PlanSchema || plan.ReaderVersion != research.HarnessV0Reader || plan.TargetSchemaVersion != string(research.SchemaProject) {
		return fmt.Errorf("unsupported or incomplete harness-v0 migration plan")
	}
	if plan.SourceRoot == "" || pathx.ValidateRelativePOSIX(plan.SourceRoot, false) != nil {
		return fmt.Errorf("migration plan source_root is invalid")
	}
	computed, err := ComputePlanHash(plan)
	if err != nil || computed != plan.ContentHash {
		return fmt.Errorf("migration plan content hash mismatch")
	}
	projectID, err := research.ImportedProjectID(plan.TreeFingerprint)
	if err != nil || projectID.String() != plan.ProjectID {
		return fmt.Errorf("migration plan Project UUIDv5 does not match tree fingerprint")
	}
	expectedArchive := "legacy/harness-v0/" + strings.TrimPrefix(plan.TreeFingerprint, "sha256:")
	if plan.Archive.Path != expectedArchive {
		return fmt.Errorf("migration archive path does not match tree fingerprint")
	}
	previousSource := ""
	for _, source := range plan.SourceFiles {
		if pathx.ValidateRelativePOSIX(source.Path, false) != nil || source.Path <= previousSource || source.Bytes < 0 {
			return fmt.Errorf("migration source file list is not strict and canonical")
		}
		previousSource = source.Path
		var cursor int64
		for _, span := range source.Spans {
			if span.StartByte != cursor || span.EndByte < span.StartByte || span.EndByte > source.Bytes {
				return fmt.Errorf("migration source span coverage is invalid for %s", source.Path)
			}
			cursor = span.EndByte
		}
		if cursor != source.Bytes {
			return fmt.Errorf("migration source spans do not cover %s", source.Path)
		}
	}
	resolutionValues, err := normalizeResolutions(plan.Resolutions)
	if err != nil || !reflect.DeepEqual(resolutionValues, plan.Resolutions) {
		return fmt.Errorf("migration plan resolutions are not normalized")
	}
	resolutionByKey := resolutionMap(plan.Resolutions)
	for _, diagnostic := range plan.Diagnostics {
		if diagnostic.State != "needs_review" || diagnostic.Key == "" {
			continue
		}
		if _, found := resolutionByKey[diagnostic.Key]; !found && plan.Applicable {
			return fmt.Errorf("applicable migration plan has unresolved review key %s", diagnostic.Key)
		}
	}
	expectedCandidates := map[string]Mapping{}
	for _, mapping := range plan.Mappings {
		if mapping.ID == "" {
			continue
		}
		kind := research.Kind(mapping.Kind)
		expected, err := research.ImportedRecordID(projectID, kind, mapping.StableSourceKey)
		if err != nil || expected.String() != mapping.ID {
			return fmt.Errorf("mapping %s UUIDv5 does not match provenance", mapping.Key)
		}
		if mapping.CandidateContent != "" {
			content, err := base64.StdEncoding.DecodeString(mapping.CandidateContent)
			if err != nil || hashBytes(content) != mapping.CandidateSHA256 {
				return fmt.Errorf("mapping %s candidate hash mismatch", mapping.Key)
			}
		}
		if mapping.Status == "selected" && mapping.Destination != "" {
			if _, duplicate := expectedCandidates[mapping.Destination]; duplicate {
				return fmt.Errorf("multiple mappings publish %s", mapping.Destination)
			}
			expectedCandidates[mapping.Destination] = mapping
		}
	}
	var importedDocuments []*record.Document
	generatedCandidates := map[string]CandidateFile{}
	seenCandidates := map[string]bool{}
	for _, file := range plan.CandidateFiles {
		if pathx.ValidateRelativePOSIX(file.Path, false) != nil || seenCandidates[file.Path] {
			return fmt.Errorf("candidate path %s is invalid or repeated", file.Path)
		}
		seenCandidates[file.Path] = true
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil || hashBytes(content) != file.SHA256 {
			return fmt.Errorf("candidate file %s hash mismatch", file.Path)
		}
		if !file.Generated {
			if err := record.ValidateRecordSize(content); err != nil {
				return err
			}
		}
		if file.Generated {
			generatedCandidates[file.Path] = file
			continue
		}
		if file.Path != record.ProjectFile {
			mapping, found := expectedCandidates[file.Path]
			if !found || mapping.CandidateSHA256 != file.SHA256 || mapping.CandidateContent != file.ContentBase64 {
				return fmt.Errorf("candidate file %s does not match a selected mapping", file.Path)
			}
			delete(expectedCandidates, file.Path)
		}
		document, err := record.DecodeImported(content)
		if err != nil {
			return fmt.Errorf("decode imported candidate %s: %w", file.Path, err)
		}
		document.Path = file.Path
		importedDocuments = append(importedDocuments, document)
	}
	if len(expectedCandidates) != 0 || !seenCandidates[record.ProjectFile] {
		return fmt.Errorf("candidate file set does not exactly match selected mappings")
	}
	inventory := record.InventoryFromMigratedDocuments("", importedDocuments)
	if !inventory.Valid() {
		return inventory.Error()
	}
	project := inventory.Project.Record.(*research.Project)
	if project.ProjectID != projectID {
		return fmt.Errorf("candidate PROJECT.md identity does not match migration plan")
	}
	generated, err := projection.Build(inventory)
	if err != nil {
		return err
	}
	if len(generatedCandidates) != len(generated) {
		return fmt.Errorf("candidate projection set is incomplete")
	}
	for _, expected := range generated {
		candidate, found := generatedCandidates[expected.Path]
		if !found || candidate.SHA256 != hashBytes(expected.Content) || candidate.ContentBase64 != base64.StdEncoding.EncodeToString(expected.Content) {
			return fmt.Errorf("generated candidate %s is not deterministic", expected.Path)
		}
	}
	manifestBytes, err := base64.StdEncoding.DecodeString(plan.Archive.ManifestContentBase64)
	if err != nil || hashBytes(manifestBytes) != plan.Archive.ManifestSHA256 {
		return fmt.Errorf("migration archive manifest hash mismatch")
	}
	var decodedManifest manifest
	metadata, err := toml.Decode(string(manifestBytes), &decodedManifest)
	if err != nil || len(metadata.Undecoded()) != 0 {
		return fmt.Errorf("migration archive manifest is not strict")
	}
	expectedManifest := harnessManifest(plan)
	if !reflect.DeepEqual(decodedManifest, expectedManifest) {
		return fmt.Errorf("migration archive manifest does not match plan source metadata")
	}
	if plan.UnifiedDiff != renderUnifiedDiff(plan.CandidateFiles) {
		return fmt.Errorf("migration unified diff does not match candidate files")
	}
	return nil
}

func encodeMigrationDocument(document *record.Document) ([]byte, error) {
	content, err := record.EncodeImported(document)
	if err != nil {
		return nil, err
	}
	if err := record.ValidateRecordSize(content); err != nil {
		return nil, err
	}
	return content, nil
}

func harnessManifest(plan *Plan) manifest {
	return manifest{
		Schema: ManifestSchema, ReaderVersion: plan.ReaderVersion,
		TreeFingerprint: plan.TreeFingerprint, SourceRoot: plan.SourceRoot,
		Files: plan.SourceFiles, Resolutions: plan.Resolutions,
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON contains trailing content")
		}
		return err
	}
	return nil
}
