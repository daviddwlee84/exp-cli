package research

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/safex"
)

// Validate applies all schema-local v1 invariants. Cross-record ownership,
// existence, and cycle checks are applied by record.Inventory.
func Validate(record Record) error {
	collector := &issueCollector{}
	if record == nil {
		collector.add("record.nil", "", "record is nil")
		return collector.err()
	}
	validateExtensions(record.GetExtensions(), collector)

	expectedSchema, schemaErr := record.GetKind().Schema()
	if schemaErr != nil || record.GetSchema() != expectedSchema {
		collector.add("record.schema", "schema", "schema %q does not match %s", record.GetSchema(), record.GetKind())
	}

	switch value := record.(type) {
	case *Project:
		validateProject(value, collector)
	case *Plan:
		validateCommon(record, &value.Common, collector)
		validatePlan(value, collector)
	case *Experiment:
		validateCommon(record, &value.Common, collector)
		validateExperiment(value, collector)
	case *Run:
		validateCommon(record, &value.Common, collector)
		validateRun(value, collector)
	case *Attempt:
		validateCommon(record, &value.Common, collector)
		validateAttempt(value, collector)
	case *Finding:
		validateCommon(record, &value.Common, collector)
		validateFinding(value, collector)
	case *Decision:
		validateCommon(record, &value.Common, collector)
		validateDecision(value, collector)
	default:
		collector.add("record.type", "", "unsupported record implementation %T", record)
	}
	return collector.err()
}

func validateProject(project *Project, collector *issueCollector) {
	if project.ProjectID.IsZero() {
		collector.add("record.id", "project_id", "project UUID is required")
	} else if !project.ProjectID.IsNative() {
		collector.add("record.id_version", "project_id", "ordinary v1 validation requires UUIDv7; UUIDv5 is reserved for a future privileged migrator")
	}
	if !singleLine(project.Name) {
		collector.add("record.name", "name", "name must be a non-empty, trimmed single line")
	}
	validateCommitSafeString(project.Name, "name", collector)
	if !validUTC(project.CreatedAt) {
		collector.add("timestamp.utc", "created_at", "created_at must be a UTC offset datetime")
	}
	if project.ExperimentsRoot != "." {
		collector.add("project.experiments_root", "experiments_root", "v1 PROJECT.md must use experiments_root = \".\"")
	}
}

func validateCommon(record Record, common *Common, collector *issueCollector) {
	if common == nil {
		collector.add("record.common", "", "common fields are missing")
		return
	}
	if common.ID.IsZero() {
		collector.add("record.id", "id", "typed ID is required")
	} else {
		if common.ID.Kind() != record.GetKind() {
			collector.add("record.id_kind", "id", "ID kind %s does not match schema kind %s", common.ID.Kind(), record.GetKind())
		}
		if !common.ID.IsNative() {
			collector.add("record.id_version", "id", "ordinary v1 validation requires UUIDv7; UUIDv5 is reserved for a future privileged migrator")
		}
	}
	if !singleLine(common.Title) {
		collector.add("record.title", "title", "title must be a non-empty, trimmed single line")
	}
	validateCommitSafeString(common.Title, "title", collector)
	if !validUTC(common.CreatedAt) {
		collector.add("timestamp.utc", "created_at", "created_at must be a UTC offset datetime")
	}
	if !validUTC(common.UpdatedAt) {
		collector.add("timestamp.utc", "updated_at", "updated_at must be a UTC offset datetime")
	} else if !common.CreatedAt.IsZero() && common.UpdatedAt.Before(common.CreatedAt) {
		collector.add("timestamp.order", "updated_at", "updated_at precedes created_at")
	}
	validateStringSet(common.Tags, "tags", func(value string) bool { return tagPattern.MatchString(value) }, collector)

	if len(common.LegacyAliases) > 0 && !hasMigrationExtension(record) {
		collector.add("alias.migration_only", "legacy_aliases", "legacy aliases require harness-v0 migration provenance")
	}
	validateStringSet(common.LegacyAliases, "legacy_aliases", func(value string) bool {
		alias, err := ParseLegacyAlias(value)
		return err == nil && alias.Kind == record.GetKind()
	}, collector)
	if len(common.LegacyAliases) > 0 && record.GetKind() != KindExperiment && record.GetKind() != KindFinding {
		collector.add("alias.kind", "legacy_aliases", "only Experiment and Finding records may have harness aliases")
	}
}

func validatePlan(plan *Plan, collector *issueCollector) {
	switch plan.Priority {
	case PriorityP1, PriorityP2, PriorityP3, PriorityUnknown:
	default:
		collector.add("plan.priority", "priority", "priority must be P1, P2, P3, or P?")
	}
	switch plan.Effort {
	case EffortS, EffortM, EffortL, EffortXL:
	default:
		collector.add("plan.effort", "effort", "effort must be S, M, L, or XL")
	}
	switch plan.State {
	case PlanQueued, PlanStarted, PlanCompleted, PlanDropped:
	default:
		collector.add("plan.state", "state", "state must be queued, started, completed, or dropped")
	}
	validateIDSet(plan.Assumptions, KindFinding, "assumptions", collector)
	hasResult := !plan.ResultingExperiment.IsZero()
	if plan.State == PlanStarted || plan.State == PlanCompleted {
		if !hasResult {
			collector.add("plan.resulting_experiment", "resulting_experiment", "started and completed plans require a resulting Experiment")
		}
	} else if hasResult {
		collector.add("plan.resulting_experiment", "resulting_experiment", "queued and dropped plans forbid a resulting Experiment")
	}
	if hasResult {
		validateReferenceKind(plan.ResultingExperiment, KindExperiment, "resulting_experiment", collector)
	}
	if !nonempty(plan.ExpectedPayoff.Summary) {
		collector.add("plan.payoff_summary", "expected_payoff.summary", "payoff summary is required")
	}
	validateCommitSafeString(plan.ExpectedPayoff.Summary, "expected_payoff.summary", collector)
	if !validMetric(plan.ExpectedPayoff.Metric) {
		collector.add("plan.payoff_metric", "expected_payoff.metric", "payoff metric must be a stable lower-case slug")
	}
	if !singleLine(plan.ExpectedPayoff.Unit) {
		collector.add("plan.payoff_unit", "expected_payoff.unit", "payoff unit must be a non-empty single line")
	}
	validateCommitSafeString(plan.ExpectedPayoff.Unit, "expected_payoff.unit", collector)
	if plan.ExpectedPayoff.Estimate != nil && !finite(*plan.ExpectedPayoff.Estimate) {
		collector.add("plan.payoff_estimate", "expected_payoff.estimate", "payoff estimate must be finite")
	}
}

func validateExperiment(experiment *Experiment, collector *issueCollector) {
	validateDesign(experiment, collector)

	lifecycleValid := true
	switch experiment.Lifecycle {
	case LifecyclePlanned, LifecycleActive, LifecycleClosed:
	default:
		lifecycleValid = false
		collector.add("experiment.lifecycle", "lifecycle", "lifecycle must be planned, active, or closed")
	}
	if lifecycleValid {
		validFields := true
		switch experiment.Lifecycle {
		case LifecyclePlanned, LifecycleActive:
			validFields = experiment.Closure == "" && experiment.Verdict == "" && experiment.ClosureDetail == nil && experiment.Conclusion == nil
		case LifecycleClosed:
			switch experiment.Closure {
			case ClosureConcluded:
				validFields = validVerdict(experiment.Verdict) && experiment.Conclusion != nil && (experiment.ClosureDetail == nil || experiment.ClosureDetail.SupersededBy.IsZero())
			case ClosureAbandoned:
				validFields = experiment.Verdict == "" && experiment.Conclusion == nil && experiment.ClosureDetail != nil && nonempty(experiment.ClosureDetail.Reason) && experiment.ClosureDetail.SupersededBy.IsZero()
			case ClosureSuperseded:
				validFields = experiment.Verdict == "" && experiment.Conclusion == nil && experiment.ClosureDetail != nil && nonempty(experiment.ClosureDetail.Reason) && !experiment.ClosureDetail.SupersededBy.IsZero()
			default:
				validFields = false
			}
		}
		if !validFields {
			collector.add("experiment.lifecycle_fields", "lifecycle", "closure, verdict, closure_detail, and conclusion do not match the lifecycle state")
		}
	}

	if experiment.ClosureDetail != nil {
		validateCommitSafeString(experiment.ClosureDetail.Reason, "closure_detail.reason", collector)
	}
	if experiment.ClosureDetail != nil && !experiment.ClosureDetail.SupersededBy.IsZero() {
		validateReferenceKind(experiment.ClosureDetail.SupersededBy, KindExperiment, "closure_detail.superseded_by", collector)
		if experiment.ClosureDetail.SupersededBy == experiment.ID {
			collector.add("reference.self", "closure_detail.superseded_by", "an Experiment cannot supersede itself")
		}
	}
	if experiment.Conclusion != nil {
		validateConclusion(experiment, collector)
	}
}

func validateDesign(experiment *Experiment, collector *issueCollector) {
	design := &experiment.Design
	for field, value := range map[string]string{
		"design.question":           design.Question,
		"design.hypothesis":         design.Hypothesis,
		"design.primary_factor":     design.PrimaryFactor,
		"design.baseline":           design.Baseline,
		"design.comparability_spec": design.ComparabilitySpec,
		"design.decision_rule":      design.DecisionRule,
	} {
		if !nonempty(value) {
			collector.add("experiment.design", field, "%s is required", strings.TrimPrefix(field, "design."))
		}
		validateCommitSafeString(value, field, collector)
	}
	switch design.Kind {
	case ExperimentSingleFactor, ExperimentFactorial, ExperimentObservational:
	default:
		collector.add("experiment.design_kind", "design.kind", "kind must be single_factor, factorial, or observational")
	}
	if design.SecondaryFactors == nil {
		collector.add("record.list_required", "design.secondary_factors", "required array must be present, even when empty")
	}
	validateNonemptyStringList(design.SecondaryFactors, "design.secondary_factors", false, collector)
	validateNonemptyStringList(design.SuccessCriteria, "design.success_criteria", true, collector)

	lockedTime := design.DesignLockedAt != nil
	lockedDigest := design.DesignDigest != ""
	if lockedTime != lockedDigest {
		collector.add("experiment.design_lock", "design", "design_locked_at and design_digest must be present or absent together")
	}
	if lockedTime {
		if !validUTC(*design.DesignLockedAt) {
			collector.add("timestamp.utc", "design.design_locked_at", "design lock timestamp must be UTC")
		} else if design.DesignLockedAt.Before(experiment.CreatedAt) || design.DesignLockedAt.After(experiment.UpdatedAt) {
			collector.add("timestamp.order", "design.design_locked_at", "design lock timestamp must fall within the record lifetime")
		}
		if !validDigest(design.DesignDigest) {
			collector.add("digest.invalid", "design.design_digest", "design digest must be lower-case sha256")
		} else if computed, err := DesignDigest(*design); err != nil || computed != design.DesignDigest {
			collector.add("experiment.design_digest", "design.design_digest", "design digest does not match the normalized design fields")
		}
	}

	previousTime := experiment.CreatedAt
	if design.DesignLockedAt != nil {
		previousTime = *design.DesignLockedAt
	}
	var previousNewDigest string
	for index, amendment := range experiment.Amendments {
		prefix := fmt.Sprintf("amendments[%d]", index)
		if !validUTC(amendment.AmendedAt) {
			collector.add("timestamp.utc", prefix+".amended_at", "amendment timestamp must be UTC")
		} else if !amendment.AmendedAt.After(previousTime) || amendment.AmendedAt.After(experiment.UpdatedAt) {
			collector.add("timestamp.order", prefix+".amended_at", "amendments must be strictly chronological, follow the design lock, and remain within the record lifetime")
		}
		previousTime = amendment.AmendedAt
		if !nonempty(amendment.Reason) {
			collector.add("experiment.amendment", prefix+".reason", "amendment reason is required")
		}
		validateCommitSafeString(amendment.Reason, prefix+".reason", collector)
		if !validDigest(amendment.PreviousDigest) || !validDigest(amendment.NewDigest) {
			collector.add("digest.invalid", prefix, "amendment digests must be lower-case sha256")
		}
		if index > 0 && amendment.PreviousDigest != previousNewDigest {
			collector.add("experiment.amendment_chain", prefix+".previous_digest", "previous_digest does not equal the preceding amendment's new_digest")
		}
		previousNewDigest = amendment.NewDigest
		validateNonemptyStringList(amendment.Changes, prefix+".changes", true, collector)
	}
	if len(experiment.Amendments) > 0 {
		if !lockedTime {
			collector.add("experiment.amendment_lock", "amendments", "amendments require a locked design")
		} else if previousNewDigest != design.DesignDigest {
			collector.add("experiment.amendment_chain", "design.design_digest", "the current design digest must equal the final amendment's new_digest")
		}
	}
}

func validateConclusion(experiment *Experiment, collector *issueCollector) {
	conclusion := experiment.Conclusion
	if !validUTC(conclusion.ConcludedAt) {
		collector.add("timestamp.utc", "conclusion.concluded_at", "conclusion timestamp must be UTC")
	} else if conclusion.ConcludedAt.Before(experiment.CreatedAt) || conclusion.ConcludedAt.After(experiment.UpdatedAt) {
		collector.add("timestamp.order", "conclusion.concluded_at", "conclusion timestamp must fall within the record lifetime")
	}
	if !nonempty(conclusion.Summary) {
		collector.add("experiment.conclusion", "conclusion.summary", "conclusion summary is required")
	}
	validateCommitSafeString(conclusion.Summary, "conclusion.summary", collector)
	if len(conclusion.Evidence) == 0 {
		collector.add("experiment.evidence", "conclusion.evidence", "a conclusion requires at least one Run evidence entry")
	}
	seen := map[string]struct{}{}
	for index, evidence := range conclusion.Evidence {
		field := fmt.Sprintf("conclusion.evidence[%d]", index)
		validateReferenceKind(evidence.Run, KindRun, field+".run", collector)
		if key := evidence.Run.String(); key != "" {
			if _, found := seen[key]; found {
				collector.add("reference.duplicate", field+".run", "Run %s appears more than once in conclusion evidence", key)
			}
			seen[key] = struct{}{}
		}
		switch evidence.Disposition {
		case EvidenceIncluded:
		case EvidenceExcluded:
			if !nonempty(evidence.Reason) {
				collector.add("experiment.evidence_reason", field+".reason", "excluded evidence requires a reason")
			}
		default:
			collector.add("experiment.evidence_disposition", field+".disposition", "disposition must be included or excluded")
		}
		validateCommitSafeString(evidence.Reason, field+".reason", collector)
	}
}

func validateRun(run *Run, collector *issueCollector) {
	validateReferenceKind(run.Experiment, KindExperiment, "experiment", collector)
	switch run.Role {
	case RunBaseline, RunCandidate, RunValidation, RunBatch:
	default:
		collector.add("run.role", "role", "role must be baseline, candidate, validation, or batch")
	}
	if !nonempty(run.Objective) {
		collector.add("run.objective", "objective", "objective is required")
	}
	validateCommitSafeString(run.Objective, "objective", collector)
	validateOptionalDigest(run.ConfigDigest, "config_digest", collector)
	validateOptionalDigest(run.DataDigest, "data_digest", collector)
	validateStringSet(run.ExpectedOutputs, "expected_outputs", func(value string) bool {
		return ValidateCommittedPath(value, false) == nil
	}, collector)
	for index, output := range run.ExpectedOutputs {
		field := fmt.Sprintf("expected_outputs[%d]", index)
		if err := ValidateCommittedPath(output, false); err != nil {
			collector.add(policyCode(err, "path.invalid"), field, "%v", err)
		}
		validateCredentialSensitiveString(output, field, collector)
	}
}

func validateAttempt(attempt *Attempt, collector *issueCollector) {
	validateReferenceKind(attempt.Run, KindRun, "run", collector)
	terminal := isTerminalState(attempt.State)
	if !terminal && !isNonterminalState(attempt.State) {
		collector.add("attempt.state", "state", "state is not a recognized operational state")
	}
	if terminal && attempt.Terminal == nil {
		collector.add("attempt.terminal", "terminal", "known terminal states require a terminal observation")
	}
	if !terminal && attempt.Terminal != nil {
		collector.add("attempt.terminal", "terminal", "nonterminal and unknown states forbid a terminal observation")
	}
	if !validSlug(attempt.Runner) || attempt.Runner == reservedProviderUnknown {
		collector.add("attempt.runner", "runner", "runner must be a non-reserved lower-case provider slug")
	} else if known, supported := KnownProviderSupportsRole(attempt.Runner, ExternalRunner); known && !supported {
		collector.add("attempt.provider_role", "runner", "known provider %q does not implement the runner role", attempt.Runner)
	}
	if !validSlug(attempt.Scheduler) || attempt.Scheduler == reservedProviderUnknown {
		collector.add("attempt.scheduler", "scheduler", "scheduler must be a non-reserved lower-case provider slug")
	} else if known, supported := KnownProviderSupportsRole(attempt.Scheduler, ExternalScheduler); known && !supported {
		collector.add("attempt.provider_role", "scheduler", "known provider %q does not implement the scheduler role", attempt.Scheduler)
	}
	if err := ValidateCommittedPath(attempt.CWD, true); err != nil {
		collector.add(policyCode(err, "path.invalid"), "cwd", "%v", err)
	}
	validateCredentialSensitiveString(attempt.CWD, "cwd", collector)
	validateArgv(attempt.Argv, collector)
	if attempt.StateReason != "" {
		validateCommitSafeString(attempt.StateReason, "state_reason", collector)
	}
	for index := range attempt.ExternalRefs {
		validateExternalRef(&attempt.ExternalRefs[index], fmt.Sprintf("external_refs[%d]", index), collector)
	}
	if attempt.Provenance != nil {
		validateProvenance(attempt, collector)
	}
	if attempt.Terminal != nil {
		validateTerminal(attempt, collector)
	}
}

func isTerminalState(state AttemptState) bool {
	switch state {
	case AttemptSucceeded, AttemptFailed, AttemptCancelled, AttemptTimedOut, AttemptPreempted, AttemptOutOfMemory:
		return true
	default:
		return false
	}
}

func isNonterminalState(state AttemptState) bool {
	switch state {
	case AttemptPlanned, AttemptQueued, AttemptBlocked, AttemptStarting, AttemptRunning, AttemptUnknown:
		return true
	default:
		return false
	}
}

func validateArgv(argv []string, collector *issueCollector) {
	if len(argv) == 0 {
		collector.add("attempt.argv", "argv", "argv must contain at least one argument")
		return
	}
	sensitive := make(map[int]struct{})
	for _, index := range safex.SensitiveArgvIndexes(argv) {
		sensitive[index] = struct{}{}
	}
	for index, argument := range argv {
		field := fmt.Sprintf("argv[%d]", index)
		if argument == "" || !singleArgument(argument) {
			collector.add("attempt.argv", field, "argument must be non-empty UTF-8 without NUL or newlines")
		}
		if _, unsafe := sensitive[index]; unsafe {
			collector.add("privacy.secret_argument", field, "credential-bearing command argument is forbidden")
		}
		validateCommitSafeString(argument, field, collector)
	}
}

func singleArgument(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validateExternalRef(reference *ExternalRef, field string, collector *issueCollector) {
	roleValid := true
	switch reference.Role {
	case ExternalRunner, ExternalScheduler, ExternalTracker, ExternalArtifact, ExternalRegistry:
	default:
		roleValid = false
		collector.add("external_ref.role", field+".role", "role is not recognized")
	}
	if !validSlug(reference.Provider) || reference.Provider == reservedProviderUnknown {
		collector.add("external_ref.provider", field+".provider", "provider must be a non-reserved lower-case slug")
	} else if roleValid {
		if known, supported := KnownProviderSupportsRole(reference.Provider, reference.Role); known && !supported {
			collector.add("external_ref.provider_role", field+".role", "known provider %q does not implement role %q", reference.Provider, reference.Role)
		}
	}
	if !validSlug(reference.Context) {
		collector.add("external_ref.context", field+".context", "context must be a non-secret lower-case slug")
	}
	if !validSlug(reference.NativeKind) {
		collector.add("external_ref.native_kind", field+".native_kind", "native_kind must be a lower-case slug")
	}
	if !singleLine(reference.NativeID) {
		collector.add("external_ref.native_id", field+".native_id", "native_id must be a non-empty single line")
	}
	validateCredentialSensitiveString(reference.NativeID, field+".native_id", collector)
	if reference.URI != "" {
		if err := ValidateCommittedURI(reference.URI); err != nil {
			collector.add(policyCode(err, "uri.invalid"), field+".uri", "%v", err)
		}
	}
	if reference.ObservedAt != nil && !validUTC(*reference.ObservedAt) {
		collector.add("timestamp.utc", field+".observed_at", "observed_at must be UTC")
	}
	for key, value := range reference.Metadata {
		metadataField := field + ".metadata." + key
		if !ValidExternalRefMetadataKey(reference.Provider, key) {
			collector.add("external_ref.metadata_key", metadataField, "metadata keys must use a lower-case provider namespace")
		}
		if credentialKey(key) {
			collector.add("privacy.secret_field", metadataField, "credential-bearing metadata keys are forbidden")
		}
		validateCommitSafeString(key, metadataField, collector)
		validateExternalMetadataValue(value, metadataField, 0, collector)
	}
	if encoded, err := json.Marshal(reference); err != nil || len(encoded) > MaxExternalRefBytes {
		collector.add("external_ref.size", field, "external reference exceeds the %d-byte bound", MaxExternalRefBytes)
	}
}

// ValidExternalRefMetadataKey reports whether key belongs to provider and uses
// the lower-case metadata-key syntax shared with provider adapters.
func ValidExternalRefMetadataKey(provider, key string) bool {
	prefix := provider + "."
	providerScoped := strings.HasPrefix(key, prefix) && len(key) > len(prefix)
	reverseDNSScoped := strings.HasSuffix(key, "."+provider) && validNamespace(key)
	if !validSlug(provider) || (!providerScoped && !reverseDNSScoped) || key[len(key)-1] == '.' {
		return false
	}
	for _, character := range key {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9', character == '.', character == '-', character == '_':
			continue
		default:
			return false
		}
	}
	return !strings.Contains(key, "..")
}

func validateExternalMetadataValue(value any, field string, depth int, collector *issueCollector) {
	if depth > 32 {
		collector.add("external_ref.metadata_depth", field, "metadata exceeds maximum nesting depth")
		return
	}
	switch typed := value.(type) {
	case nil:
		collector.add("external_ref.metadata_value", field, "metadata values cannot be null")
	case bool, int64:
	case float64:
		if !finite(typed) {
			collector.add("external_ref.metadata_number", field, "metadata number must be finite")
		}
	case string:
		validateCredentialSensitiveString(typed, field, collector)
	case []any:
		for index, nested := range typed {
			validateExternalMetadataValue(nested, fmt.Sprintf("%s[%d]", field, index), depth+1, collector)
		}
	case map[string]any:
		for key, nested := range typed {
			child := field + "." + key
			if credentialKey(key) {
				collector.add("privacy.secret_field", child, "credential-bearing metadata keys are forbidden")
			}
			validateCommitSafeString(key, child, collector)
			validateExternalMetadataValue(nested, child, depth+1, collector)
		}
	default:
		reflected := reflect.ValueOf(value)
		switch reflected.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		case reflect.Float32:
			if !finite(reflected.Float()) {
				collector.add("external_ref.metadata_number", field, "metadata number must be finite")
			}
		case reflect.Slice, reflect.Array:
			for index := 0; index < reflected.Len(); index++ {
				validateExternalMetadataValue(reflected.Index(index).Interface(), fmt.Sprintf("%s[%d]", field, index), depth+1, collector)
			}
		case reflect.Map:
			if reflected.Type().Key().Kind() != reflect.String {
				collector.add("external_ref.metadata_value", field, "metadata map keys must be strings")
				return
			}
			iterator := reflected.MapRange()
			for iterator.Next() {
				key := iterator.Key().String()
				child := field + "." + key
				if credentialKey(key) {
					collector.add("privacy.secret_field", child, "credential-bearing metadata keys are forbidden")
				}
				validateCommitSafeString(key, child, collector)
				validateExternalMetadataValue(iterator.Value().Interface(), child, depth+1, collector)
			}
		default:
			collector.add("external_ref.metadata_value", field, "metadata value of type %T is not in the JSON/TOML common subset", value)
		}
	}
}

func validateProvenance(attempt *Attempt, collector *issueCollector) {
	provenance := attempt.Provenance
	if !validUTC(provenance.CapturedAt) {
		collector.add("timestamp.utc", "provenance.captured_at", "captured_at must be UTC")
	} else if provenance.CapturedAt.Before(attempt.CreatedAt) || provenance.CapturedAt.After(attempt.UpdatedAt) {
		collector.add("timestamp.order", "provenance.captured_at", "captured_at must fall within the record lifetime")
	}
	if !gitCommitPattern.MatchString(provenance.GitCommit) {
		collector.add("provenance.git_commit", "provenance.git_commit", "git_commit must be a full lower-case SHA-1 or SHA-256 object ID")
	}
	if !provenance.GitDirty && provenance.DirtyDigest != "" {
		collector.add("provenance.dirty_digest", "provenance.dirty_digest", "dirty_digest is forbidden when git_dirty is false")
	}
	validateOptionalDigest(provenance.DirtyDigest, "provenance.dirty_digest", collector)
	validateOptionalDigest(provenance.ConfigDigest, "provenance.config_digest", collector)
	validateOptionalDigest(provenance.DataDigest, "provenance.data_digest", collector)
	validateOptionalDigest(provenance.EnvironmentDigest, "provenance.environment_digest", collector)
	switch provenance.Reproducibility {
	case ReproducibilityExact, ReproducibilityBounded, ReproducibilityPartial, ReproducibilityUnknown:
	default:
		collector.add("provenance.reproducibility", "provenance.reproducibility", "reproducibility is not recognized")
	}
}

func validateTerminal(attempt *Attempt, collector *issueCollector) {
	terminal := attempt.Terminal
	if !validSlug(terminal.Source) {
		collector.add("attempt.terminal_source", "terminal.source", "terminal source must be a lower-case slug")
	}
	if !validUTC(terminal.ObservedAt) {
		collector.add("timestamp.utc", "terminal.observed_at", "observed_at must be UTC")
	}
	if !validUTC(terminal.EndedAt) {
		collector.add("timestamp.utc", "terminal.ended_at", "ended_at must be UTC")
	}
	if terminal.StartedAt != nil && !validUTC(*terminal.StartedAt) {
		collector.add("timestamp.utc", "terminal.started_at", "started_at must be UTC")
	}
	if terminal.StartedAt != nil && terminal.EndedAt.Before(*terminal.StartedAt) {
		collector.add("timestamp.order", "terminal.ended_at", "ended_at precedes started_at")
	}
	if terminal.ObservedAt.Before(terminal.EndedAt) {
		collector.add("timestamp.order", "terminal.observed_at", "observed_at precedes ended_at")
	}
	if terminal.StartedAt != nil && terminal.StartedAt.Before(attempt.CreatedAt) {
		collector.add("timestamp.order", "terminal.started_at", "started_at precedes record creation")
	}
	if terminal.EndedAt.Before(attempt.CreatedAt) {
		collector.add("timestamp.order", "terminal.ended_at", "ended_at precedes record creation")
	}
	if terminal.ObservedAt.After(attempt.UpdatedAt) {
		collector.add("timestamp.order", "terminal.observed_at", "observed_at follows record updated_at")
	}
	if terminal.Signal != "" && !singleLine(terminal.Signal) {
		collector.add("attempt.signal", "terminal.signal", "signal must be a single-line name")
	}
	validateCommitSafeString(terminal.Signal, "terminal.signal", collector)
}

func validateFinding(finding *Finding, collector *issueCollector) {
	if !nonempty(finding.Statement) {
		collector.add("finding.statement", "statement", "statement is required")
	}
	validateCommitSafeString(finding.Statement, "statement", collector)
	if !nonempty(finding.Scope) {
		collector.add("finding.scope", "scope", "scope is required")
	}
	validateCommitSafeString(finding.Scope, "scope", collector)
	if len(finding.Evidence) == 0 {
		collector.add("finding.evidence", "evidence", "a Finding requires at least one evidence entry")
	}
	seenEvidence := map[string]struct{}{}
	for index, evidence := range finding.Evidence {
		field := fmt.Sprintf("evidence[%d]", index)
		expected := KindUnknown
		switch evidence.Kind {
		case FindingEvidenceRun:
			expected = KindRun
		case FindingEvidenceExperiment:
			expected = KindExperiment
			if !hasMigrationExtension(finding) {
				collector.add("finding.coarse_evidence", field+".kind", "Experiment evidence is reserved for migrated Findings")
			}
		default:
			collector.add("finding.evidence_kind", field+".kind", "kind must be run or migration-only experiment")
		}
		if expected != KindUnknown {
			validateReferenceKind(evidence.Ref, expected, field+".ref", collector)
		}
		validateCommitSafeString(evidence.Detail, field+".detail", collector)
		key := string(evidence.Kind) + "\x00" + evidence.Ref.String()
		if _, found := seenEvidence[key]; found {
			collector.add("reference.duplicate", field+".ref", "duplicate evidence reference")
		}
		seenEvidence[key] = struct{}{}
	}
	validateIDSet(finding.Weakens, KindFinding, "weakens", collector)
	validateIDSet(finding.Overturns, KindFinding, "overturns", collector)
	weakened := map[ID]struct{}{}
	for _, target := range finding.Weakens {
		weakened[target] = struct{}{}
		if target == finding.ID {
			collector.add("reference.self", "weakens", "a Finding cannot weaken itself")
		}
	}
	for _, target := range finding.Overturns {
		if target == finding.ID {
			collector.add("reference.self", "overturns", "a Finding cannot overturn itself")
		}
		if _, found := weakened[target]; found {
			collector.add("finding.relation_overlap", "overturns", "a target cannot occur in both weakens and overturns")
		}
	}
}

func validateDecision(decision *Decision, collector *issueCollector) {
	if !nonempty(decision.Statement) {
		collector.add("decision.statement", "statement", "statement is required")
	}
	validateCommitSafeString(decision.Statement, "statement", collector)
	if len(decision.BasedOn) == 0 {
		collector.add("decision.based_on", "based_on", "a Decision requires at least one Finding")
	}
	validateIDSet(decision.BasedOn, KindFinding, "based_on", collector)
	if !nonempty(decision.Action) {
		collector.add("decision.action", "action", "action is required")
	} else {
		validateCommitSafeString(decision.Action, "action", collector)
	}
	if !validUTC(decision.EffectiveAt) {
		collector.add("timestamp.utc", "effective_at", "effective_at must be UTC")
	} else if decision.EffectiveAt.Before(decision.CreatedAt) {
		collector.add("timestamp.order", "effective_at", "effective_at precedes record creation")
	}
	validateIDSet(decision.Supersedes, KindDecision, "supersedes", collector)
	for _, target := range decision.Supersedes {
		if target == decision.ID {
			collector.add("reference.self", "supersedes", "a Decision cannot supersede itself")
		}
	}
}

func validateReferenceKind(id ID, expected Kind, field string, collector *issueCollector) {
	if id.IsZero() {
		collector.add("reference.required", field, "%s reference is required", expected)
		return
	}
	if id.Kind() != expected {
		collector.add("reference.wrong_kind", field, "reference %s is %s, expected %s", id, id.Kind(), expected)
	}
	if !id.IsNative() {
		collector.add("reference.id_version", field, "ordinary v1 references require UUIDv7; UUIDv5 is reserved for a future privileged migrator")
	}
}

func validateIDSet(values []ID, expected Kind, field string, collector *issueCollector) {
	seen := map[ID]struct{}{}
	for index, value := range values {
		itemField := fmt.Sprintf("%s[%d]", field, index)
		validateReferenceKind(value, expected, itemField, collector)
		if _, found := seen[value]; found {
			collector.add("reference.duplicate", itemField, "duplicate reference %s", value)
		}
		seen[value] = struct{}{}
	}
}

func validateStringSet(values []string, field string, valid func(string) bool, collector *issueCollector) {
	seen := map[string]struct{}{}
	for index, value := range values {
		itemField := fmt.Sprintf("%s[%d]", field, index)
		if !valid(value) {
			collector.add("record.set_value", itemField, "value is invalid")
		}
		if _, found := seen[value]; found {
			collector.add("record.set_duplicate", itemField, "duplicate value")
		}
		seen[value] = struct{}{}
	}
}

func validateNonemptyStringList(values []string, field string, required bool, collector *issueCollector) {
	if required && len(values) == 0 {
		collector.add("record.list_required", field, "at least one entry is required")
	}
	seen := map[string]struct{}{}
	for index, value := range values {
		itemField := fmt.Sprintf("%s[%d]", field, index)
		if !nonempty(value) {
			collector.add("record.list_value", itemField, "entry must be non-empty")
		}
		validateCommitSafeString(value, itemField, collector)
		if _, found := seen[value]; found {
			collector.add("record.list_duplicate", itemField, "duplicate entry")
		}
		seen[value] = struct{}{}
	}
}

func validateOptionalDigest(value, field string, collector *issueCollector) {
	if value != "" && !validDigest(value) {
		collector.add("digest.invalid", field, "digest must be lower-case sha256")
	}
}

func validVerdict(verdict Verdict) bool {
	switch verdict {
	case VerdictSupported, VerdictRefuted, VerdictInconclusive, VerdictInvalid:
		return true
	default:
		return false
	}
}

// SortPlans applies the deterministic plan view order: state, priority, then ID.
func SortPlans(plans []*Plan) {
	stateOrder := map[PlanState]int{PlanQueued: 0, PlanStarted: 1, PlanCompleted: 2, PlanDropped: 3}
	priorityOrder := map[Priority]int{PriorityP1: 0, PriorityP2: 1, PriorityP3: 2, PriorityUnknown: 3}
	sort.SliceStable(plans, func(i, j int) bool {
		if stateOrder[plans[i].State] != stateOrder[plans[j].State] {
			return stateOrder[plans[i].State] < stateOrder[plans[j].State]
		}
		if priorityOrder[plans[i].Priority] != priorityOrder[plans[j].Priority] {
			return priorityOrder[plans[i].Priority] < priorityOrder[plans[j].Priority]
		}
		return plans[i].ID.String() < plans[j].ID.String()
	})
}
