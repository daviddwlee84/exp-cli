package research

import (
	"fmt"
	"math"
	"strings"
)

func validatePolicy(policy *Policy, collector *issueCollector) {
	if !validUTC(policy.CreatedAt) {
		collector.add("timestamp.utc", "created_at", "created_at must be UTC")
	}
	if !validUTC(policy.UpdatedAt) {
		collector.add("timestamp.utc", "updated_at", "updated_at must be UTC")
	} else if !policy.CreatedAt.IsZero() && policy.UpdatedAt.Before(policy.CreatedAt) {
		collector.add("timestamp.order", "updated_at", "updated_at precedes created_at")
	}
	switch policy.Autonomy {
	case AutonomyManual, AutonomyShadow, AutonomyAssisted, AutonomyLimited:
	default:
		collector.add("policy.autonomy", "autonomy", "autonomy is not recognized")
	}
	if !unitInterval(policy.ExploitShare) || !unitInterval(policy.ExploreShare) || math.Abs(policy.ExploitShare+policy.ExploreShare-1) > 1e-9 {
		collector.add("policy.lane_share", "exploit_share", "exploit_share and explore_share must be finite fractions summing to 1")
	}
	if policy.ScoreFormula != "utility-v1" {
		collector.add("policy.score_formula", "score_formula", "this build supports only the utility-v1 transparent score formula")
	}
	switch policy.TiePolicy {
	case QueueTieKeepIncumbent, QueueTieHumanReview:
	default:
		collector.add("policy.tie_policy", "tie_policy", "tie_policy is not recognized")
	}
	if !policy.PromotionRequiresHuman {
		collector.add("policy.promotion_human", "promotion_requires_human", "v1 promotion policy must require human approval")
	}
	validateSlugSet(policy.Taxonomy.Domains, "taxonomy.domains", true, collector)
	validateSlugSet(policy.Taxonomy.Work, "taxonomy.work", true, collector)
	validateSlugSet(policy.Taxonomy.Methods, "taxonomy.methods", true, collector)
	validateSlugSet(policy.Taxonomy.Components, "taxonomy.components", true, collector)
	saturation := policy.ClusterSaturation
	if !positiveFinite(saturation.BudgetHours) {
		collector.add("policy.cluster_budget", "cluster_saturation.budget_hours", "budget_hours must be finite and positive")
	}
	if saturation.PlateauWindow == 0 {
		collector.add("policy.plateau_window", "cluster_saturation.plateau_window", "plateau_window must be positive")
	}
	if !finite(saturation.MinimumImprovement) || saturation.MinimumImprovement < 0 {
		collector.add("policy.minimum_improvement", "cluster_saturation.minimum_improvement", "minimum_improvement must be finite and non-negative")
	}
	if !unitInterval(saturation.MinimumProbability) {
		collector.add("policy.minimum_probability", "cluster_saturation.minimum_probability", "minimum_probability must be between 0 and 1")
	}
	seenClusters := map[string]struct{}{}
	for index, cluster := range policy.Clusters {
		field := fmt.Sprintf("clusters[%d]", index)
		if !validSlug(cluster.Name) {
			collector.add("policy.cluster", field+".name", "cluster name must be a lower-case slug")
		}
		if _, found := seenClusters[cluster.Name]; found {
			collector.add("record.set_duplicate", field+".name", "cluster occurs more than once")
		}
		seenClusters[cluster.Name] = struct{}{}
		switch cluster.State {
		case ClusterOpen, ClusterSaturated:
		default:
			collector.add("policy.cluster_state", field+".state", "cluster state is not recognized")
		}
		if !positiveFinite(cluster.BudgetHours) || cluster.PlateauWindow == 0 || !finite(cluster.MinimumImprovement) || cluster.MinimumImprovement < 0 || !unitInterval(cluster.MinimumProbability) {
			collector.add("policy.cluster_threshold", field, "cluster thresholds must use a positive budget/window, non-negative finite improvement, and probability between 0 and 1")
		}
		if cluster.State == ClusterSaturated && !nonempty(cluster.ReopenCondition) {
			collector.add("policy.reopen_condition", field+".reopen_condition", "saturated clusters require a reopen condition")
		}
		validateCommitSafeString(cluster.ReopenCondition, field+".reopen_condition", collector)
	}
}

func validateIdea(idea *Idea, collector *issueCollector) {
	switch idea.State {
	case IdeaProposed, IdeaDeveloping, IdeaQualified, IdeaQueued, IdeaDismissed, IdeaMerged:
	default:
		collector.add("idea.state", "state", "state is not recognized")
	}
	if !nonempty(idea.Summary) {
		collector.add("idea.summary", "summary", "summary is required")
	}
	validateCommitSafeString(idea.Summary, "summary", collector)
	if !singleLine(idea.ProposedBy) {
		collector.add("idea.proposed_by", "proposed_by", "proposed_by must be a non-empty single line")
	}
	validateCommitSafeString(idea.ProposedBy, "proposed_by", collector)
	if !validSlug(idea.PrimaryCluster) {
		collector.add("classification.cluster", "primary_cluster", "primary_cluster must be a lower-case slug")
	}
	validateClassification(&idea.Classification, "classification", collector)
	validateIDSet(idea.Parents, KindIdea, "parents", collector)
	for _, parent := range idea.Parents {
		if parent == idea.ID {
			collector.add("reference.self", "parents", "an Idea cannot derive from itself")
		}
	}
	if !idea.ResultingPlan.IsZero() {
		validateReferenceKind(idea.ResultingPlan, KindPlan, "resulting_plan", collector)
	}
	if !idea.MergedInto.IsZero() {
		validateReferenceKind(idea.MergedInto, KindIdea, "merged_into", collector)
		if idea.MergedInto == idea.ID {
			collector.add("reference.self", "merged_into", "an Idea cannot merge into itself")
		}
	}
	if idea.State == IdeaMerged && idea.MergedInto.IsZero() {
		collector.add("idea.merged_into", "merged_into", "merged ideas require merged_into")
	}
	if idea.State != IdeaMerged && !idea.MergedInto.IsZero() {
		collector.add("idea.merged_into", "merged_into", "only merged ideas may set merged_into")
	}
	if (idea.State == IdeaQualified || idea.State == IdeaQueued) && idea.ResultingPlan.IsZero() {
		collector.add("idea.resulting_plan", "resulting_plan", "qualified and queued ideas require a resulting Plan")
	}
	if (idea.State == IdeaProposed || idea.State == IdeaDeveloping) && !idea.ResultingPlan.IsZero() {
		collector.add("idea.resulting_plan", "resulting_plan", "proposed and developing ideas cannot already point to a Plan")
	}
}

func validateResourcePool(pool *ResourcePool, collector *issueCollector) {
	if pool.Capacity == 0 {
		collector.add("pool.capacity", "capacity", "capacity must be positive")
	}
	if !singleLine(pool.Unit) {
		collector.add("pool.unit", "unit", "unit must be a non-empty single line")
	}
	validateCommitSafeString(pool.Unit, "unit", collector)
	if !validSlug(pool.Bottleneck) {
		collector.add("pool.bottleneck", "bottleneck", "bottleneck must be a lower-case slug")
	}
	if pool.CostPerHour != nil && (!finite(*pool.CostPerHour) || *pool.CostPerHour < 0) {
		collector.add("pool.cost", "cost_per_hour", "cost_per_hour must be finite and non-negative")
	}
}

func validateQueue(queue *Queue, collector *issueCollector) {
	if queue.Revision == 0 {
		collector.add("queue.revision", "revision", "queue revision must be positive")
	}
	if queue.Partitions == nil {
		collector.add("record.list_required", "partitions", "partitions must be present, even when empty")
	}
	seenPartitions := map[string]struct{}{}
	seenPlans := map[ID]struct{}{}
	for partitionIndex := range queue.Partitions {
		partition := &queue.Partitions[partitionIndex]
		prefix := fmt.Sprintf("partitions[%d]", partitionIndex)
		validateReferenceKind(partition.Pool, KindResourcePool, prefix+".pool", collector)
		validateLane(partition.Lane, prefix+".lane", collector)
		key := partition.Pool.String() + "\x00" + string(partition.Lane)
		if _, found := seenPartitions[key]; found {
			collector.add("queue.partition_duplicate", prefix, "pool and lane partition occurs more than once")
		}
		seenPartitions[key] = struct{}{}
		if partition.Entries == nil {
			collector.add("record.list_required", prefix+".entries", "entries must be present, even when empty")
		}
		for entryIndex := range partition.Entries {
			entry := &partition.Entries[entryIndex]
			field := fmt.Sprintf("%s.entries[%d]", prefix, entryIndex)
			validateReferenceKind(entry.Plan, KindPlan, field+".plan", collector)
			if _, found := seenPlans[entry.Plan]; found {
				collector.add("queue.plan_duplicate", field+".plan", "a Plan may appear in only one queue partition")
			}
			seenPlans[entry.Plan] = struct{}{}
			if !validDigest(entry.PlanRevision) {
				collector.add("queue.plan_revision", field+".plan_revision", "plan_revision must be a lower-case sha256 revision")
			}
			if !finite(entry.Score) {
				collector.add("queue.score", field+".score", "score must be finite")
			}
			if !validUTC(entry.InsertedAt) {
				collector.add("timestamp.utc", field+".inserted_at", "inserted_at must be UTC")
			} else if entry.InsertedAt.After(queue.UpdatedAt) {
				collector.add("timestamp.order", field+".inserted_at", "inserted_at follows queue updated_at")
			}
		}
	}
}

func validateQueueAdvice(advice *QueueAdvice, collector *issueCollector) {
	validateImmutable(&advice.Common, "QueueAdvice", collector)
	validateReferenceKind(advice.Queue, KindQueue, "queue", collector)
	if advice.QueueRevision == 0 {
		collector.add("queue.revision", "queue_revision", "queue_revision must be positive")
	}
	validateReferenceKind(advice.CandidatePlan, KindPlan, "candidate_plan", collector)
	validateReferenceKind(advice.Pool, KindResourcePool, "pool", collector)
	validateLane(advice.Lane, "lane", collector)
	validateIDSet(advice.ListwiseOrder, KindPlan, "listwise_order", collector)
	if len(advice.ListwiseOrder) == 0 {
		collector.add("advice.order", "listwise_order", "listwise_order must not be empty")
	}
	if advice.ProposedPosition >= uint64(len(advice.ListwiseOrder)) && len(advice.ListwiseOrder) > 0 {
		collector.add("advice.position", "proposed_position", "proposed_position is outside listwise_order")
	}
	foundCandidate := false
	for _, plan := range advice.ListwiseOrder {
		foundCandidate = foundCandidate || plan == advice.CandidatePlan
	}
	if !foundCandidate {
		collector.add("advice.candidate", "listwise_order", "listwise_order must contain candidate_plan")
	}
	validateQueueScore(advice.Score, "score", collector)
	if !singleLine(advice.Model) {
		collector.add("advice.model", "model", "model must be a non-empty single line")
	}
	validateCommitSafeString(advice.Model, "model", collector)
	if !validDigest(advice.PromptDigest) {
		collector.add("advice.prompt_digest", "prompt_digest", "prompt_digest must be a lower-case sha256")
	}
	if !nonempty(advice.Rationale) {
		collector.add("advice.rationale", "rationale", "rationale is required")
	}
	validateCommitSafeString(advice.Rationale, "rationale", collector)
}

func validateBattle(battle *Battle, collector *issueCollector) {
	validateImmutable(&battle.Common, "Battle", collector)
	validateReferenceKind(battle.Queue, KindQueue, "queue", collector)
	if battle.QueueRevision == 0 {
		collector.add("queue.revision", "queue_revision", "queue_revision must be positive")
	}
	if !battle.Advice.IsZero() {
		validateReferenceKind(battle.Advice, KindQueueAdvice, "advice", collector)
	}
	validateReferenceKind(battle.CandidatePlan, KindPlan, "candidate_plan", collector)
	validateReferenceKind(battle.IncumbentPlan, KindPlan, "incumbent_plan", collector)
	if battle.CandidatePlan == battle.IncumbentPlan {
		collector.add("battle.same_plan", "incumbent_plan", "candidate and incumbent must differ")
	}
	validateReferenceKind(battle.Pool, KindResourcePool, "pool", collector)
	validateLane(battle.Lane, "lane", collector)
	validateBattleChoice(battle.OrderAB, "order_ab", collector)
	validateBattleChoice(battle.OrderBA, "order_ba", collector)
	switch battle.Outcome {
	case BattleCandidateWins, BattleIncumbentWins, BattleTie, BattleHumanReview:
	default:
		collector.add("battle.outcome", "outcome", "outcome is not recognized")
	}
	if battle.OrderAB != battle.OrderBA && battle.Outcome != BattleHumanReview {
		collector.add("battle.disagreement", "outcome", "order-swapped disagreement requires human_review")
	}
	if battle.OrderAB == BattleChooseCandidate && battle.OrderBA == BattleChooseCandidate && battle.Outcome != BattleCandidateWins {
		collector.add("battle.outcome", "outcome", "two candidate choices require candidate_wins")
	}
	if battle.OrderAB == BattleChooseIncumbent && battle.OrderBA == BattleChooseIncumbent && battle.Outcome != BattleIncumbentWins {
		collector.add("battle.outcome", "outcome", "two incumbent choices require incumbent_wins")
	}
	if battle.OrderAB == BattleChooseTie && battle.OrderBA == BattleChooseTie && battle.Outcome != BattleTie {
		collector.add("battle.outcome", "outcome", "two ties require tie")
	}
	if !unitInterval(battle.Confidence) {
		collector.add("battle.confidence", "confidence", "confidence must be between 0 and 1")
	}
	if !nonempty(battle.Rationale) {
		collector.add("battle.rationale", "rationale", "rationale is required")
	}
	validateCommitSafeString(battle.Rationale, "rationale", collector)
}

func validateEvaluationSpec(spec *EvaluationSpec, collector *issueCollector) {
	switch spec.Purpose {
	case EvaluationScientific, EvaluationPromotion:
	default:
		collector.add("evaluation_spec.purpose", "purpose", "purpose is not recognized")
	}
	if !singleLine(spec.Dataset) {
		collector.add("evaluation_spec.dataset", "dataset", "dataset must be a non-empty single line")
	}
	validateCommitSafeString(spec.Dataset, "dataset", collector)
	if !nonempty(spec.Protocol) {
		collector.add("evaluation_spec.protocol", "protocol", "protocol is required")
	}
	validateCommitSafeString(spec.Protocol, "protocol", collector)
	validateMetricSpecs(spec.Metrics, collector)
	validateReferenceKind(spec.BudgetPool, KindResourcePool, "budget_pool", collector)
	if !positiveFinite(spec.BudgetHours) {
		collector.add("evaluation_spec.budget", "budget_hours", "budget_hours must be finite and positive")
	}
	if spec.SealedAt != nil {
		if !validUTC(*spec.SealedAt) {
			collector.add("timestamp.utc", "sealed_at", "sealed_at must be UTC")
		} else if spec.SealedAt.Before(spec.CreatedAt) || spec.SealedAt.After(spec.UpdatedAt) {
			collector.add("timestamp.order", "sealed_at", "sealed_at must fall within the record lifetime")
		}
	}
	if spec.Purpose == EvaluationPromotion && spec.SealedAt == nil {
		collector.add("evaluation_spec.sealed", "sealed_at", "promotion evaluation specs must be sealed")
	}
	if spec.Purpose == EvaluationPromotion {
		for index, metric := range spec.Metrics {
			if metric.Threshold == nil {
				collector.add("evaluation_spec.promotion_threshold", fmt.Sprintf("metrics[%d].threshold", index), "promotion metrics require sealed pass/fail thresholds")
			}
		}
	}
}

func validateEvaluation(evaluation *Evaluation, collector *issueCollector) {
	validateImmutable(&evaluation.Common, "Evaluation", collector)
	validateReferenceKind(evaluation.Spec, KindEvaluationSpec, "spec", collector)
	if evaluation.Subject.IsZero() {
		collector.add("reference.required", "subject", "subject reference is required")
	} else {
		switch evaluation.Subject.Kind() {
		case KindExperiment, KindCandidate, KindRelease:
		default:
			collector.add("reference.wrong_kind", "subject", "evaluation subject must be an Experiment, Candidate, or Release")
		}
		if !evaluation.Subject.IsNative() {
			collector.add("reference.id_version", "subject", "ordinary references require UUIDv7")
		}
	}
	switch evaluation.Outcome {
	case EvaluationPassed, EvaluationFailed, EvaluationInvalid:
	default:
		collector.add("evaluation.outcome", "outcome", "outcome is not recognized")
	}
	if !validUTC(evaluation.EvaluatedAt) {
		collector.add("timestamp.utc", "evaluated_at", "evaluated_at must be UTC")
	} else if evaluation.EvaluatedAt.Before(evaluation.CreatedAt) || evaluation.EvaluatedAt.After(evaluation.UpdatedAt) {
		collector.add("timestamp.order", "evaluated_at", "evaluated_at must fall within the record lifetime")
	}
	if len(evaluation.Metrics) == 0 {
		collector.add("evaluation.metrics", "metrics", "at least one metric is required")
	}
	seen := map[string]struct{}{}
	for index, metric := range evaluation.Metrics {
		field := fmt.Sprintf("metrics[%d]", index)
		if !validMetric(metric.Name) {
			collector.add("evaluation.metric", field+".name", "metric name must be a stable slug")
		}
		if !singleLine(metric.Unit) {
			collector.add("evaluation.unit", field+".unit", "metric unit must be a non-empty single line")
		}
		if !finite(metric.Value) {
			collector.add("evaluation.value", field+".value", "metric value must be finite")
		}
		if _, found := seen[metric.Name]; found {
			collector.add("record.set_duplicate", field+".name", "metric occurs more than once")
		}
		seen[metric.Name] = struct{}{}
	}
	for index := range evaluation.ExternalRefs {
		validateExternalRef(&evaluation.ExternalRefs[index], fmt.Sprintf("external_refs[%d]", index), collector)
	}
	if !nonempty(evaluation.Summary) {
		collector.add("evaluation.summary", "summary", "summary is required")
	}
	validateCommitSafeString(evaluation.Summary, "summary", collector)
}

func validateCandidate(candidate *Candidate, collector *issueCollector) {
	validateReferenceKind(candidate.Experiment, KindExperiment, "experiment", collector)
	validateReferenceKind(candidate.Evaluation, KindEvaluation, "evaluation", collector)
	validateIDSet(candidate.Parents, KindCandidate, "parents", collector)
	for _, parent := range candidate.Parents {
		if parent == candidate.ID {
			collector.add("reference.self", "parents", "a Candidate cannot derive from itself")
		}
	}
	if !gitCommitPattern.MatchString(candidate.GitCommit) {
		collector.add("candidate.git_commit", "git_commit", "git_commit must be a full lower-case SHA-1 or SHA-256 object ID")
	}
	validatePathList(candidate.ChangeSet, "change_set", true, collector)
	for index := range candidate.ExternalRefs {
		validateExternalRef(&candidate.ExternalRefs[index], fmt.Sprintf("external_refs[%d]", index), collector)
	}
}

func validateRelease(release *Release, collector *issueCollector) {
	if !validSlug(release.Target) {
		collector.add("release.target", "target", "target must be a lower-case slug")
	}
	if !singleLine(release.Version) {
		collector.add("release.version", "version", "version must be a non-empty single line")
	}
	validateCommitSafeString(release.Version, "version", collector)
	switch release.State {
	case ReleaseDraft, ReleaseValidated, ReleaseRetired:
	default:
		collector.add("release.state", "state", "state is not recognized")
	}
	if len(release.Slots) == 0 {
		collector.add("release.slots", "slots", "at least one typed slot is required")
	}
	seenNames := map[string]struct{}{}
	seenCandidates := map[ID]struct{}{}
	for index, slot := range release.Slots {
		field := fmt.Sprintf("slots[%d]", index)
		if !validSlug(slot.Name) {
			collector.add("release.slot", field+".name", "slot name must be a lower-case slug")
		}
		if _, found := seenNames[slot.Name]; found {
			collector.add("record.set_duplicate", field+".name", "slot name occurs more than once")
		}
		seenNames[slot.Name] = struct{}{}
		validateReferenceKind(slot.Candidate, KindCandidate, field+".candidate", collector)
		seenCandidates[slot.Candidate] = struct{}{}
	}
	if len(seenCandidates) > 1 && (release.CombinationExperiment.IsZero() || release.CombinationEvaluation.IsZero()) {
		collector.add("release.combination", "combination_experiment", "a release combining candidates requires both a combination Experiment and its passing Evaluation")
	}
	if !release.CombinationExperiment.IsZero() {
		validateReferenceKind(release.CombinationExperiment, KindExperiment, "combination_experiment", collector)
	}
	if !release.CombinationEvaluation.IsZero() {
		validateReferenceKind(release.CombinationEvaluation, KindEvaluation, "combination_evaluation", collector)
	}
	if release.CombinationExperiment.IsZero() != release.CombinationEvaluation.IsZero() {
		collector.add("release.combination", "combination_evaluation", "combination Experiment and Evaluation must be recorded together")
	}
	if release.State == ReleaseValidated && release.Evaluation.IsZero() {
		collector.add("release.evaluation", "evaluation", "validated releases require an Evaluation")
	}
	if !release.Evaluation.IsZero() {
		validateReferenceKind(release.Evaluation, KindEvaluation, "evaluation", collector)
	}
}

func validatePromotionSpec(spec *PromotionSpec, collector *issueCollector) {
	if !validSlug(spec.Target) {
		collector.add("promotion.target", "target", "target must be a lower-case slug")
	}
	validateReferenceKind(spec.EvaluationSpec, KindEvaluationSpec, "evaluation_spec", collector)
	if !validUTC(spec.SealedAt) {
		collector.add("timestamp.utc", "sealed_at", "sealed_at must be UTC")
	} else if spec.SealedAt.Before(spec.CreatedAt) || spec.SealedAt.After(spec.UpdatedAt) {
		collector.add("timestamp.order", "sealed_at", "sealed_at must fall within the record lifetime")
	}
	if !positiveFinite(spec.HoldoutBudgetHours) {
		collector.add("promotion.holdout_budget", "holdout_budget_hours", "holdout budget must be finite and positive")
	}
	if !spec.HumanApprovalRequired {
		collector.add("promotion.human_required", "human_approval_required", "v1 promotion specs must require human approval")
	}
}

func validatePromotion(promotion *Promotion, collector *issueCollector) {
	validateImmutable(&promotion.Common, "Promotion", collector)
	if !validSlug(promotion.Target) {
		collector.add("promotion.target", "target", "target must be a lower-case slug")
	}
	validateReferenceKind(promotion.Spec, KindPromotionSpec, "spec", collector)
	validateReferenceKind(promotion.Challenger, KindRelease, "challenger", collector)
	if !promotion.Incumbent.IsZero() {
		validateReferenceKind(promotion.Incumbent, KindRelease, "incumbent", collector)
	}
	if promotion.Challenger == promotion.Incumbent {
		collector.add("promotion.same_release", "incumbent", "challenger and incumbent must differ")
	}
	validateReferenceKind(promotion.Evaluation, KindEvaluation, "evaluation", collector)
	switch promotion.Outcome {
	case PromotionAccepted, PromotionRejected, PromotionRolledBack:
	default:
		collector.add("promotion.outcome", "outcome", "outcome is not recognized")
	}
	if !validUTC(promotion.AppliedAt) {
		collector.add("timestamp.utc", "applied_at", "applied_at must be UTC")
	} else if promotion.AppliedAt.Before(promotion.CreatedAt) || promotion.AppliedAt.After(promotion.UpdatedAt) {
		collector.add("timestamp.order", "applied_at", "applied_at must fall within the record lifetime")
	}
	if !promotion.Previous.IsZero() {
		validateReferenceKind(promotion.Previous, KindPromotion, "previous", collector)
		if promotion.Previous == promotion.ID {
			collector.add("reference.self", "previous", "a Promotion cannot follow itself")
		}
	}
	if !singleLine(promotion.ApprovedBy) {
		collector.add("promotion.approved_by", "approved_by", "approved_by must identify a human approver")
	}
	lowerApprover := strings.ToLower(promotion.ApprovedBy)
	if strings.HasPrefix(lowerApprover, "agent:") || strings.HasPrefix(lowerApprover, "bot:") || strings.HasPrefix(lowerApprover, "model:") {
		collector.add("promotion.human_approval", "approved_by", "autonomous agents and models cannot approve a Promotion")
	}
	validateCommitSafeString(promotion.ApprovedBy, "approved_by", collector)
}

func validatePlanVersion(plan *Plan, collector *issueCollector) {
	switch plan.Schema {
	case SchemaPlan:
		if !plan.Idea.IsZero() || plan.PrimaryCluster != "" || plan.Classification != nil || len(plan.Dependencies) > 0 || len(plan.Resources) > 0 || plan.Utility != nil {
			collector.add("record.schema_field", "schema", "v2 planning fields require exp.plan/v2")
		}
	case SchemaPlanV2:
		if !plan.Idea.IsZero() {
			validateReferenceKind(plan.Idea, KindIdea, "idea", collector)
		}
		if !validSlug(plan.PrimaryCluster) {
			collector.add("classification.cluster", "primary_cluster", "primary_cluster must be a lower-case slug")
		}
		if plan.Classification == nil {
			collector.add("plan.classification", "classification", "v2 Plans require classification")
		} else {
			validateClassification(plan.Classification, "classification", collector)
		}
		seenDependencies := map[ID]struct{}{}
		for index, dependency := range plan.Dependencies {
			field := fmt.Sprintf("dependencies[%d]", index)
			validateReferenceKind(dependency.Finding, KindFinding, field+".finding", collector)
			if _, found := seenDependencies[dependency.Finding]; found {
				collector.add("reference.duplicate", field+".finding", "Finding dependency occurs more than once")
			}
			seenDependencies[dependency.Finding] = struct{}{}
			if !validDigest(dependency.Revision) {
				collector.add("plan.dependency_revision", field+".revision", "revision must be a lower-case sha256")
			}
			if !validDigest(dependency.BeliefDigest) {
				collector.add("plan.belief_digest", field+".belief_digest", "belief_digest must be a lower-case sha256")
			}
		}
		seenPools := map[ID]struct{}{}
		if len(plan.Resources) != 1 {
			collector.add("plan.resources", "resources", "v2 Plans require exactly one ResourcePool estimate; represent coupled resources as a composite pool")
		}
		for index, need := range plan.Resources {
			field := fmt.Sprintf("resources[%d]", index)
			validateReferenceKind(need.Pool, KindResourcePool, field+".pool", collector)
			if _, found := seenPools[need.Pool]; found {
				collector.add("reference.duplicate", field+".pool", "ResourcePool occurs more than once")
			}
			seenPools[need.Pool] = struct{}{}
			if need.Units == 0 {
				collector.add("plan.resource_units", field+".units", "units must be positive")
			}
			if !positiveFinite(need.EstimatedHours) {
				collector.add("plan.resource_hours", field+".estimated_hours", "estimated_hours must be finite and positive")
			}
		}
		if plan.Utility == nil {
			collector.add("plan.utility", "utility", "v2 Plans require a utility estimate")
		} else {
			if !unitInterval(plan.Utility.Probability) {
				collector.add("plan.utility_probability", "utility.probability", "probability must be between 0 and 1")
			}
			for field, value := range map[string]float64{
				"utility.impact": plan.Utility.Impact, "utility.information_gain": plan.Utility.InformationGain,
				"utility.unblock_value": plan.Utility.UnblockValue, "utility.risk_penalty": plan.Utility.RiskPenalty,
			} {
				if !finite(value) || value < 0 {
					collector.add("plan.utility_value", field, "utility component must be finite and non-negative")
				}
			}
		}
	}
}

func validateExperimentVersion(experiment *Experiment, collector *issueCollector) {
	switch experiment.Schema {
	case SchemaExperiment:
		if len(experiment.Parents) > 0 || len(experiment.CandidateInputs) > 0 {
			collector.add("record.schema_field", "schema", "lineage and candidate inputs require exp.experiment/v2")
		}
	case SchemaExperimentV2:
		validateIDSet(experiment.Parents, KindExperiment, "parents", collector)
		for _, parent := range experiment.Parents {
			if parent == experiment.ID {
				collector.add("reference.self", "parents", "an Experiment cannot descend from itself")
			}
		}
		validateIDSet(experiment.CandidateInputs, KindCandidate, "candidate_inputs", collector)
		if experiment.Design.Kind == ExperimentCombination && len(experiment.CandidateInputs) < 2 {
			collector.add("experiment.combination_inputs", "candidate_inputs", "combination experiments require at least two Candidate inputs")
		}
		if experiment.Design.Kind != ExperimentCombination && len(experiment.CandidateInputs) > 0 {
			collector.add("experiment.candidate_inputs", "candidate_inputs", "only combination experiments may declare candidate_inputs")
		}
	}
}

func validateAttemptVersion(attempt *Attempt, collector *issueCollector) {
	switch attempt.Schema {
	case SchemaAttempt:
		if !attempt.Pool.IsZero() || !attempt.Queue.IsZero() || attempt.QueueRevision != 0 || attempt.Lane != "" || attempt.DispatchID != "" || attempt.BaseCommit != "" || attempt.HeadCommit != "" || len(attempt.ChangeSet) > 0 {
			collector.add("record.schema_field", "schema", "dispatch and ChangeSet fields require exp.attempt/v2")
		}
	case SchemaAttemptV2:
		validateReferenceKind(attempt.Pool, KindResourcePool, "pool", collector)
		validateReferenceKind(attempt.Queue, KindQueue, "queue", collector)
		if attempt.QueueRevision == 0 {
			collector.add("queue.revision", "queue_revision", "queue_revision must be positive")
		}
		validateLane(attempt.Lane, "lane", collector)
		if !singleLine(attempt.DispatchID) {
			collector.add("attempt.dispatch_id", "dispatch_id", "dispatch_id must be a non-empty single line")
		}
		validateCommitSafeString(attempt.DispatchID, "dispatch_id", collector)
		if !gitCommitPattern.MatchString(attempt.BaseCommit) {
			collector.add("attempt.base_commit", "base_commit", "base_commit must be a full lower-case Git object ID")
		}
		if !gitCommitPattern.MatchString(attempt.HeadCommit) {
			collector.add("attempt.head_commit", "head_commit", "head_commit must be a full lower-case Git object ID")
		}
		validatePathList(attempt.ChangeSet, "change_set", true, collector)
	}
}

func validateClassification(classification *Classification, prefix string, collector *issueCollector) {
	for name, value := range map[string]string{
		"domain": classification.Domain, "work": classification.Work,
		"method": classification.Method, "component": classification.Component,
	} {
		if !validSlug(value) {
			collector.add("classification.value", prefix+"."+name, "classification value must be a lower-case slug")
		}
	}
	validateLane(classification.Lane, prefix+".lane", collector)
	switch classification.Risk {
	case RiskLow, RiskMedium, RiskHigh:
	default:
		collector.add("classification.risk", prefix+".risk", "risk is not recognized")
	}
	switch classification.Horizon {
	case HorizonShort, HorizonMedium, HorizonLong:
	default:
		collector.add("classification.horizon", prefix+".horizon", "horizon is not recognized")
	}
	switch classification.Origin {
	case OriginHuman, OriginAgent, OriginHybrid, OriginImported:
	default:
		collector.add("classification.origin", prefix+".origin", "origin is not recognized")
	}
}

func validateLane(lane ResearchLane, field string, collector *issueCollector) {
	switch lane {
	case LaneExploit, LaneExplore:
	default:
		collector.add("queue.lane", field, "lane must be exploit or explore")
	}
}

func validateQueueScore(score QueueScore, prefix string, collector *issueCollector) {
	for name, value := range map[string]float64{
		"expected_utility": score.ExpectedUtility, "information_gain": score.InformationGain,
		"unblock_value": score.UnblockValue, "risk_penalty": score.RiskPenalty,
		"pool_hours": score.PoolHours, "total": score.Total,
	} {
		if !finite(value) {
			collector.add("queue.score", prefix+"."+name, "score component must be finite")
		}
	}
	if score.PoolHours <= 0 {
		collector.add("queue.pool_hours", prefix+".pool_hours", "pool_hours must be positive")
	}
}

func validateBattleChoice(choice BattleChoice, field string, collector *issueCollector) {
	switch choice {
	case BattleChooseCandidate, BattleChooseIncumbent, BattleChooseTie:
	default:
		collector.add("battle.choice", field, "choice is not recognized")
	}
}

func validateMetricSpecs(metrics []MetricSpec, collector *issueCollector) {
	if len(metrics) == 0 {
		collector.add("evaluation_spec.metrics", "metrics", "at least one metric is required")
	}
	seen := map[string]struct{}{}
	for index, metric := range metrics {
		field := fmt.Sprintf("metrics[%d]", index)
		if !validMetric(metric.Name) {
			collector.add("evaluation_spec.metric", field+".name", "metric name must be a stable slug")
		}
		if _, found := seen[metric.Name]; found {
			collector.add("record.set_duplicate", field+".name", "metric occurs more than once")
		}
		seen[metric.Name] = struct{}{}
		if !singleLine(metric.Unit) {
			collector.add("evaluation_spec.unit", field+".unit", "metric unit must be a non-empty single line")
		}
		validateCommitSafeString(metric.Unit, field+".unit", collector)
		switch metric.Direction {
		case MetricMaximize, MetricMinimize:
		default:
			collector.add("evaluation_spec.direction", field+".direction", "direction must be maximize or minimize")
		}
		if metric.Threshold != nil && !finite(*metric.Threshold) {
			collector.add("evaluation_spec.threshold", field+".threshold", "threshold must be finite")
		}
	}
}

func validatePathList(values []string, field string, required bool, collector *issueCollector) {
	if required && len(values) == 0 {
		collector.add("record.list_required", field, "at least one path is required")
	}
	seen := map[string]struct{}{}
	for index, value := range values {
		item := fmt.Sprintf("%s[%d]", field, index)
		if err := ValidateCommittedPath(value, false); err != nil {
			collector.add(policyCode(err, "path.invalid"), item, "%v", err)
		}
		validateCredentialSensitiveString(value, item, collector)
		if _, found := seen[value]; found {
			collector.add("record.set_duplicate", item, "path occurs more than once")
		}
		seen[value] = struct{}{}
	}
}

func validateSlugSet(values []string, field string, required bool, collector *issueCollector) {
	if required && len(values) == 0 {
		collector.add("record.list_required", field, "at least one value is required")
	}
	validateStringSet(values, field, validSlug, collector)
}

func validateImmutable(common *Common, name string, collector *issueCollector) {
	if !common.CreatedAt.Equal(common.UpdatedAt) {
		collector.add("record.immutable", "updated_at", "%s audit records are immutable; updated_at must equal created_at", name)
	}
}

func positiveFinite(value float64) bool { return finite(value) && value > 0 }
func unitInterval(value float64) bool   { return finite(value) && value >= 0 && value <= 1 }
