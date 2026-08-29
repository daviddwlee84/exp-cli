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
	case *Plan:
		sortIDs(value.Assumptions)
		value.Assumptions = nilIDsIfEmpty(value.Assumptions)
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
	case *Finding:
		sortIDs(value.Weakens)
		value.Weakens = nilIDsIfEmpty(value.Weakens)
		sortIDs(value.Overturns)
		value.Overturns = nilIDsIfEmpty(value.Overturns)
	case *Decision:
		sortIDs(value.BasedOn)
		sortIDs(value.Supersedes)
		value.Supersedes = nilIDsIfEmpty(value.Supersedes)
		value.EffectiveAt = utc(value.EffectiveAt)
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
	case *Plan:
		if source == nil {
			return (*Plan)(nil)
		}
		out := *source
		cloneCommon(&out.Common, &source.Common)
		out.Assumptions = append([]ID(nil), source.Assumptions...)
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
