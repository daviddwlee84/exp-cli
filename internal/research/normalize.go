package research

import (
	"reflect"
	"sort"
	"strings"
	"time"
)

// Normalize mutates record into its deterministic v1 representation. Callers
// should Validate before Normalize so duplicate set members are not hidden.
func Normalize(record Record) {
	if record == nil {
		return
	}
	if common := record.GetCommon(); common != nil {
		common.CreatedAt = utc(common.CreatedAt)
		common.UpdatedAt = utc(common.UpdatedAt)
		sort.Strings(common.LegacyAliases)
		common.LegacyAliases = nilIfEmpty(common.LegacyAliases)
		sort.Strings(common.Tags)
		common.Tags = nilIfEmpty(common.Tags)
	}

	switch value := record.(type) {
	case *Project:
		value.CreatedAt = utc(value.CreatedAt)
	case *Policy:
		value.CreatedAt = utc(value.CreatedAt)
		value.UpdatedAt = utc(value.UpdatedAt)
		sort.Strings(value.Taxonomy.Domains)
		sort.Strings(value.Taxonomy.Work)
		sort.Strings(value.Taxonomy.Methods)
		sort.Strings(value.Taxonomy.Components)
		sort.SliceStable(value.Clusters, func(i, j int) bool { return value.Clusters[i].Name < value.Clusters[j].Name })
	case *Idea:
		sortIDs(value.Parents)
		value.Parents = nilIDsIfEmpty(value.Parents)
	case *ResourcePool:
	case *Queue:
		sort.SliceStable(value.Partitions, func(i, j int) bool {
			if value.Partitions[i].Pool != value.Partitions[j].Pool {
				return value.Partitions[i].Pool.String() < value.Partitions[j].Pool.String()
			}
			return value.Partitions[i].Lane < value.Partitions[j].Lane
		})
		for i := range value.Partitions {
			for j := range value.Partitions[i].Entries {
				entry := &value.Partitions[i].Entries[j]
				entry.PlanRevision = strings.ToLower(entry.PlanRevision)
				entry.InsertedAt = utc(entry.InsertedAt)
			}
		}
	case *QueueAdvice:
		value.PromptDigest = strings.ToLower(value.PromptDigest)
	case *Battle:
	case *Plan:
		sortIDs(value.Assumptions)
		value.Assumptions = nilIDsIfEmpty(value.Assumptions)
		sort.SliceStable(value.Dependencies, func(i, j int) bool {
			return value.Dependencies[i].Finding.String() < value.Dependencies[j].Finding.String()
		})
		for i := range value.Dependencies {
			value.Dependencies[i].Revision = strings.ToLower(value.Dependencies[i].Revision)
			value.Dependencies[i].BeliefDigest = strings.ToLower(value.Dependencies[i].BeliefDigest)
		}
		sort.SliceStable(value.Resources, func(i, j int) bool { return value.Resources[i].Pool.String() < value.Resources[j].Pool.String() })
	case *Experiment:
		if value.Design.DesignLockedAt != nil {
			t := utc(*value.Design.DesignLockedAt)
			value.Design.DesignLockedAt = &t
		}
		value.Design.DesignDigest = strings.ToLower(value.Design.DesignDigest)
		for i := range value.Amendments {
			value.Amendments[i].AmendedAt = utc(value.Amendments[i].AmendedAt)
			value.Amendments[i].PreviousDigest = strings.ToLower(value.Amendments[i].PreviousDigest)
			value.Amendments[i].NewDigest = strings.ToLower(value.Amendments[i].NewDigest)
		}
		if len(value.Amendments) == 0 {
			value.Amendments = nil
		}
		if value.Conclusion != nil {
			value.Conclusion.ConcludedAt = utc(value.Conclusion.ConcludedAt)
		}
		sortIDs(value.Parents)
		value.Parents = nilIDsIfEmpty(value.Parents)
		sortIDs(value.CandidateInputs)
		value.CandidateInputs = nilIDsIfEmpty(value.CandidateInputs)
	case *Run:
		value.ConfigDigest = strings.ToLower(value.ConfigDigest)
		value.DataDigest = strings.ToLower(value.DataDigest)
		sort.Strings(value.ExpectedOutputs)
		value.ExpectedOutputs = nilIfEmpty(value.ExpectedOutputs)
		if len(value.Seeds) == 0 {
			value.Seeds = nil
		}
	case *Attempt:
		for i := range value.ExternalRefs {
			if value.ExternalRefs[i].ObservedAt != nil {
				t := utc(*value.ExternalRefs[i].ObservedAt)
				value.ExternalRefs[i].ObservedAt = &t
			}
		}
		if value.Provenance != nil {
			value.Provenance.CapturedAt = utc(value.Provenance.CapturedAt)
			value.Provenance.DirtyDigest = strings.ToLower(value.Provenance.DirtyDigest)
			value.Provenance.ConfigDigest = strings.ToLower(value.Provenance.ConfigDigest)
			value.Provenance.DataDigest = strings.ToLower(value.Provenance.DataDigest)
			value.Provenance.EnvironmentDigest = strings.ToLower(value.Provenance.EnvironmentDigest)
		}
		if value.Terminal != nil {
			value.Terminal.ObservedAt = utc(value.Terminal.ObservedAt)
			value.Terminal.EndedAt = utc(value.Terminal.EndedAt)
			if value.Terminal.StartedAt != nil {
				t := utc(*value.Terminal.StartedAt)
				value.Terminal.StartedAt = &t
			}
		}
		sort.Strings(value.ChangeSet)
		value.ChangeSet = nilIfEmpty(value.ChangeSet)
	case *EvaluationSpec:
		if value.SealedAt != nil {
			t := utc(*value.SealedAt)
			value.SealedAt = &t
		}
		sort.SliceStable(value.Metrics, func(i, j int) bool { return value.Metrics[i].Name < value.Metrics[j].Name })
	case *Evaluation:
		value.EvaluatedAt = utc(value.EvaluatedAt)
		sort.SliceStable(value.Metrics, func(i, j int) bool { return value.Metrics[i].Name < value.Metrics[j].Name })
		normalizeExternalRefs(value.ExternalRefs)
	case *Finding:
		sortIDs(value.Weakens)
		value.Weakens = nilIDsIfEmpty(value.Weakens)
		sortIDs(value.Overturns)
		value.Overturns = nilIDsIfEmpty(value.Overturns)
	case *Candidate:
		sortIDs(value.Parents)
		value.Parents = nilIDsIfEmpty(value.Parents)
		sort.Strings(value.ChangeSet)
		normalizeExternalRefs(value.ExternalRefs)
	case *Release:
		sort.SliceStable(value.Slots, func(i, j int) bool { return value.Slots[i].Name < value.Slots[j].Name })
	case *PromotionSpec:
		value.SealedAt = utc(value.SealedAt)
	case *Promotion:
		value.AppliedAt = utc(value.AppliedAt)
	case *Decision:
		sortIDs(value.BasedOn)
		sortIDs(value.Supersedes)
		value.Supersedes = nilIDsIfEmpty(value.Supersedes)
		value.EffectiveAt = utc(value.EffectiveAt)
	}
}

func normalizeExternalRefs(references []ExternalRef) {
	for i := range references {
		if references[i].ObservedAt != nil {
			observed := utc(*references[i].ObservedAt)
			references[i].ObservedAt = &observed
		}
	}
}

func utc(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC()
}

func sortIDs(values []ID) {
	sort.SliceStable(values, func(i, j int) bool { return values[i].String() < values[j].String() })
}

func nilIfEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func nilIDsIfEmpty(values []ID) []ID {
	if len(values) == 0 {
		return nil
	}
	return values
}

// Clone returns a deep copy suitable for normalization and encoding.
func Clone(record Record) Record {
	switch source := record.(type) {
	case *Project:
		if source == nil {
			return (*Project)(nil)
		}
		out := *source
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Policy:
		if source == nil {
			return (*Policy)(nil)
		}
		out := *source
		out.Taxonomy.Domains = cloneStrings(source.Taxonomy.Domains)
		out.Taxonomy.Work = cloneStrings(source.Taxonomy.Work)
		out.Taxonomy.Methods = cloneStrings(source.Taxonomy.Methods)
		out.Taxonomy.Components = cloneStrings(source.Taxonomy.Components)
		out.Clusters = append([]ClusterPolicy(nil), source.Clusters...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Idea:
		if source == nil {
			return (*Idea)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Parents = append([]ID(nil), source.Parents...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *ResourcePool:
		if source == nil {
			return (*ResourcePool)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		if source.CostPerHour != nil {
			cost := *source.CostPerHour
			out.CostPerHour = &cost
		}
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Queue:
		if source == nil {
			return (*Queue)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		if source.Partitions != nil {
			out.Partitions = make([]QueuePartition, len(source.Partitions))
			copy(out.Partitions, source.Partitions)
		}
		for i := range out.Partitions {
			if source.Partitions[i].Entries != nil {
				out.Partitions[i].Entries = make([]QueueEntry, len(source.Partitions[i].Entries))
				copy(out.Partitions[i].Entries, source.Partitions[i].Entries)
			}
		}
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *QueueAdvice:
		if source == nil {
			return (*QueueAdvice)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.ListwiseOrder = append([]ID(nil), source.ListwiseOrder...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Battle:
		if source == nil {
			return (*Battle)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Plan:
		if source == nil {
			return (*Plan)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Assumptions = append([]ID(nil), source.Assumptions...)
		out.Dependencies = append([]FindingDependency(nil), source.Dependencies...)
		out.Resources = append([]ResourceNeed(nil), source.Resources...)
		if source.Classification != nil {
			classification := *source.Classification
			out.Classification = &classification
		}
		if source.Utility != nil {
			utility := *source.Utility
			out.Utility = &utility
		}
		out.Extensions = cloneExtensions(source.Extensions)
		if source.ExpectedPayoff.Estimate != nil {
			estimate := *source.ExpectedPayoff.Estimate
			out.ExpectedPayoff.Estimate = &estimate
		}
		return &out
	case *Experiment:
		if source == nil {
			return (*Experiment)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Design.SecondaryFactors = cloneStrings(source.Design.SecondaryFactors)
		out.Design.SuccessCriteria = cloneStrings(source.Design.SuccessCriteria)
		if source.Design.DesignLockedAt != nil {
			locked := *source.Design.DesignLockedAt
			out.Design.DesignLockedAt = &locked
		}
		out.Amendments = append([]Amendment(nil), source.Amendments...)
		for i := range out.Amendments {
			out.Amendments[i].Changes = append([]string(nil), source.Amendments[i].Changes...)
		}
		if source.ClosureDetail != nil {
			detail := *source.ClosureDetail
			out.ClosureDetail = &detail
		}
		if source.Conclusion != nil {
			conclusion := *source.Conclusion
			conclusion.Evidence = append([]ConclusionEvidence(nil), source.Conclusion.Evidence...)
			out.Conclusion = &conclusion
		}
		out.Parents = append([]ID(nil), source.Parents...)
		out.CandidateInputs = append([]ID(nil), source.CandidateInputs...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Run:
		if source == nil {
			return (*Run)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Seeds = append([]int64(nil), source.Seeds...)
		out.ExpectedOutputs = append([]string(nil), source.ExpectedOutputs...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Attempt:
		if source == nil {
			return (*Attempt)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Argv = append([]string(nil), source.Argv...)
		out.ChangeSet = append([]string(nil), source.ChangeSet...)
		out.ExternalRefs = append([]ExternalRef(nil), source.ExternalRefs...)
		for i := range out.ExternalRefs {
			if source.ExternalRefs[i].ObservedAt != nil {
				observed := *source.ExternalRefs[i].ObservedAt
				out.ExternalRefs[i].ObservedAt = &observed
			}
			out.ExternalRefs[i].Metadata = cloneStringMap(source.ExternalRefs[i].Metadata)
		}
		if source.Provenance != nil {
			provenance := *source.Provenance
			out.Provenance = &provenance
		}
		if source.Terminal != nil {
			terminal := *source.Terminal
			if source.Terminal.StartedAt != nil {
				started := *source.Terminal.StartedAt
				terminal.StartedAt = &started
			}
			if source.Terminal.ExitCode != nil {
				exitCode := *source.Terminal.ExitCode
				terminal.ExitCode = &exitCode
			}
			out.Terminal = &terminal
		}
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *EvaluationSpec:
		if source == nil {
			return (*EvaluationSpec)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Metrics = append([]MetricSpec(nil), source.Metrics...)
		for i := range out.Metrics {
			if source.Metrics[i].Threshold != nil {
				threshold := *source.Metrics[i].Threshold
				out.Metrics[i].Threshold = &threshold
			}
		}
		if source.SealedAt != nil {
			sealed := *source.SealedAt
			out.SealedAt = &sealed
		}
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Evaluation:
		if source == nil {
			return (*Evaluation)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Metrics = append([]MetricValue(nil), source.Metrics...)
		out.ExternalRefs = cloneExternalRefs(source.ExternalRefs)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Finding:
		if source == nil {
			return (*Finding)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Weakens = append([]ID(nil), source.Weakens...)
		out.Overturns = append([]ID(nil), source.Overturns...)
		out.Evidence = append([]FindingEvidence(nil), source.Evidence...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Candidate:
		if source == nil {
			return (*Candidate)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Parents = append([]ID(nil), source.Parents...)
		out.ChangeSet = append([]string(nil), source.ChangeSet...)
		out.ExternalRefs = cloneExternalRefs(source.ExternalRefs)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Release:
		if source == nil {
			return (*Release)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Slots = append([]ReleaseSlot(nil), source.Slots...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *PromotionSpec:
		if source == nil {
			return (*PromotionSpec)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Promotion:
		if source == nil {
			return (*Promotion)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	case *Decision:
		if source == nil {
			return (*Decision)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.BasedOn = append([]ID(nil), source.BasedOn...)
		out.Supersedes = append([]ID(nil), source.Supersedes...)
		out.Extensions = cloneExtensions(source.Extensions)
		return &out
	default:
		return nil
	}
}

func cloneExternalRefs(source []ExternalRef) []ExternalRef {
	if source == nil {
		return nil
	}
	out := append([]ExternalRef(nil), source...)
	for i := range out {
		if source[i].ObservedAt != nil {
			observed := *source[i].ObservedAt
			out[i].ObservedAt = &observed
		}
		out[i].Metadata = cloneStringMap(source[i].Metadata)
	}
	return out
}

func cloneCommon(destination, source *Common) {
	*destination = *source
	destination.LegacyAliases = cloneStrings(source.LegacyAliases)
	destination.Tags = cloneStrings(source.Tags)
}

func cloneStrings(source []string) []string {
	if source == nil {
		return nil
	}
	out := make([]string, len(source))
	copy(out, source)
	return out
}

func cloneExtensions(source Extensions) Extensions {
	if source == nil {
		return nil
	}
	out := make(Extensions, len(source))
	for namespace, value := range source {
		out[namespace] = cloneStringMap(value)
	}
	return out
}

func cloneStringMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneStringMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneAny(typed[i])
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i := range typed {
			out[i] = cloneStringMap(typed[i])
		}
		return out
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice {
		out := reflect.MakeSlice(reflected.Type(), reflected.Len(), reflected.Len())
		reflect.Copy(out, reflected)
		return out.Interface()
	}
	return value
}

// SetRecordID sets the immutable identity while constructing a new record.
func SetRecordID(record Record, id ID) bool {
	if record == nil || record.GetKind() != id.Kind() || record.GetCommon() == nil {
		return false
	}
	record.GetCommon().ID = id
	return true
}
