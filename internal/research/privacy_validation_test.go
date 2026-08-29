package research

import (
	"strings"
	"testing"
	"time"
)

func TestPlanProjectedTextRejectsCredentialMaterial(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	base := &Plan{
		Common:         Common{Schema: SchemaPlan, ID: mustID(t, "plan_01a01e66-f8e0-7202-8000-000000000202"), Title: "Compare tokenization methods", CreatedAt: now, UpdatedAt: now},
		Priority:       PriorityP1,
		Effort:         EffortS,
		State:          PlanQueued,
		ExpectedPayoff: ExpectedPayoff{Summary: "Measure whether the tokenizer improves accuracy", Metric: "macro_f1", Unit: "score"},
	}
	if err := Validate(base); err != nil {
		t.Fatalf("safe scientific prose: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"title credential URL", func(plan *Plan) { plan.Title = "Inspect https://alice:pw@example.invalid/run" }},
		{"payoff credential URL", func(plan *Plan) {
			plan.ExpectedPayoff.Summary = "Inspect https://example.invalid/run?access_token=CANARY"
		}},
		{"payoff authorization header", func(plan *Plan) { plan.ExpectedPayoff.Summary = "Authorization: Bearer CANARY" }},
		{"payoff JSON authorization", func(plan *Plan) { plan.ExpectedPayoff.Summary = `{"authorization":"Bearer CANARY"}` }},
		{"unit cookie", func(plan *Plan) { plan.ExpectedPayoff.Unit = "Cookie: session=CANARY" }},
		{"private key material", func(plan *Plan) { plan.ExpectedPayoff.Summary = "-----BEGIN PRIVATE KEY-----\nCANARY" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := Clone(base).(*Plan)
			test.mutate(plan)
			if err := Validate(plan); !hasIssueCode(err, "privacy.secret") {
				t.Fatalf("credential-bearing Plan validated: %v", err)
			}
		})
	}
}

func TestCommitSafeTextAllowsOrdinaryScientificProse(t *testing.T) {
	for _, value := range []string{
		"Compare authorization mechanisms without recording credentials.",
		"Measure tokenizer quality and cookie classification accuracy.",
		"The preprocessor emits token=CLS as a categorical label.",
		"See https://example.invalid/paper?section=methods for the public protocol.",
		"The access token parameter must never be committed.",
	} {
		if err := ValidateCommitSafeText(value); err != nil {
			t.Errorf("ordinary prose %q rejected: %v", value, err)
		}
	}
}

func TestAttemptArgvRejectsEverySensitiveShape(t *testing.T) {
	tests := map[string][]string{
		"separated auth token":   {"runner", "--auth-token", "CANARY"},
		"inline auth token":      {"runner", "--auth-token=CANARY"},
		"short header":           {"curl", "-H", "Authorization: Bearer CANARY"},
		"joined short header":    {"curl", "-HCookie: session=CANARY"},
		"long header":            {"curl", "--header", "Cookie: session=CANARY"},
		"inline long header":     {"curl", "--header=Authorization: Bearer CANARY"},
		"separated user":         {"curl", "--user", "alice:CANARY"},
		"inline user":            {"curl", "--user=alice:CANARY"},
		"joined short user":      {"curl", "-ualice:CANARY"},
		"direct environment":     {"env", "SERVICE_API_KEY=CANARY", "runner"},
		"separated environment":  {"runner", "--env", "SERVICE_PASSWORD=CANARY"},
		"inline environment":     {"runner", "--environment=serviceAuthToken=CANARY"},
		"camel-case secret flag": {"runner", "--databasePassword", "CANARY"},
		"auth value flag":        {"runner", "--auth_value", "CANARY"},
		"session id flag":        {"runner", "--session_id=CANARY"},
		"bare bearer pair":       {"runner", "Bearer", "CANARY"},
		"bare basic argument":    {"runner", "Basic Q0FOQVJZOnNlY3JldA=="},
		"known key variant":      {"runner", "--aws-secret-access-key", "CANARY"},
		"key file variant":       {"runner", "--key-file", "credentials.txt"},
		"ssh key variant":        {"runner", "--ssh-key", "credentials.txt"},
		"password variant":       {"runner", "--db-password=CANARY"},
		"password file variant":  {"runner", "--db-password-file", "credentials.txt"},
		"token file variant":     {"runner", "--auth-token-file", "credentials.txt"},
	}
	for name, argv := range tests {
		t.Run(name, func(t *testing.T) {
			attempt := validPrivacyAttempt(t)
			attempt.Argv = argv
			if err := Validate(attempt); err == nil || !hasIssueCode(err, "privacy.secret_argument") {
				t.Fatalf("sensitive argv validated: %v", err)
			}
		})
	}
}

func TestAttemptArgvAllowsOrdinaryScientificArguments(t *testing.T) {
	attempt := validPrivacyAttempt(t)
	attempt.Argv = []string{
		"python3", "train.py", "--tokenizer", "word-piece", "--max-tokens", "2048",
		"--header", "Accept: application/json", "SPLIT=validation", "token=[CLS]", "token", "count",
	}
	if err := Validate(attempt); err != nil {
		t.Fatalf("safe scientific argv rejected: %v", err)
	}
}

func TestExternalRefRejectsCredentialIdentityURIAndMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExternalRef)
	}{
		{"native id", func(ref *ExternalRef) { ref.NativeID = "token=CANARY" }},
		{"URI userinfo", func(ref *ExternalRef) { ref.URI = "https://alice:CANARY@example.invalid/run" }},
		{"URI path", func(ref *ExternalRef) { ref.URI = "https://example.invalid/token=CANARY" }},
		{"URI encoded path", func(ref *ExternalRef) { ref.URI = "https://example.invalid/access_token%3DCANARY" }},
		{"URI fragment", func(ref *ExternalRef) { ref.URI = "https://example.invalid/#access_token=CANARY" }},
		{"URI nested userinfo fragment", func(ref *ExternalRef) { ref.URI = "https://example.invalid/#https://alice:CANARY@other.invalid/run" }},
		{"metadata key", func(ref *ExternalRef) { ref.Metadata = map[string]any{"mlflow.token": "CANARY"} }},
		{"metadata key value", func(ref *ExternalRef) { ref.Metadata = map[string]any{"mlflow.auth_value=CANARY": "unsafe"} }},
		{"metadata value", func(ref *ExternalRef) { ref.Metadata = map[string]any{"mlflow.note": "Authorization: Bearer CANARY"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempt := validPrivacyAttempt(t)
			ref := &attempt.ExternalRefs[0]
			test.mutate(ref)
			err := Validate(attempt)
			if err == nil {
				t.Fatal("credential-bearing ExternalRef validated")
			}
			if strings.Contains(err.Error(), "CANARY") {
				t.Fatalf("validation diagnostic leaked credential canary: %v", err)
			}
		})
	}
}

func TestReservedUnknownProviderIsRejectedButFutureSlugsRemainValid(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*Attempt)
		code   string
	}{
		"runner": {
			mutate: func(attempt *Attempt) { attempt.Runner = "unknown" },
			code:   "attempt.runner",
		},
		"scheduler": {
			mutate: func(attempt *Attempt) { attempt.Scheduler = "unknown" },
			code:   "attempt.scheduler",
		},
		"external reference": {
			mutate: func(attempt *Attempt) { attempt.ExternalRefs[0].Provider = "unknown" },
			code:   "external_ref.provider",
		},
	} {
		t.Run(name, func(t *testing.T) {
			attempt := validPrivacyAttempt(t)
			test.mutate(attempt)
			if err := Validate(attempt); !hasIssueCode(err, test.code) {
				t.Fatalf("reserved provider validation = %v, want %s", err, test.code)
			}
		})
	}

	attempt := validPrivacyAttempt(t)
	attempt.Runner = "future-runner"
	attempt.Scheduler = "future-scheduler"
	attempt.ExternalRefs[0].Provider = "future-tracker"
	if err := Validate(attempt); err != nil {
		t.Fatalf("future provider slugs rejected: %v", err)
	}
}

func TestKnownProviderRolesRejectImpossibleClaimsAndAllowFutureProviders(t *testing.T) {
	attempt := validPrivacyAttempt(t)
	attempt.Runner = "pueue"
	if err := Validate(attempt); !hasIssueCode(err, "attempt.provider_role") {
		t.Fatalf("known invalid runner role = %v", err)
	}

	attempt = validPrivacyAttempt(t)
	attempt.Scheduler = "mlflow"
	if err := Validate(attempt); !hasIssueCode(err, "attempt.provider_role") {
		t.Fatalf("known invalid scheduler role = %v", err)
	}

	attempt = validPrivacyAttempt(t)
	attempt.ExternalRefs[0].Role = ExternalScheduler
	if err := Validate(attempt); !hasIssueCode(err, "external_ref.provider_role") {
		t.Fatalf("known invalid ExternalRef role = %v", err)
	}

	attempt = validPrivacyAttempt(t)
	attempt.Runner = "future-runner"
	attempt.Scheduler = "future-scheduler"
	attempt.ExternalRefs[0].Provider = "future-tracker"
	if err := Validate(attempt); err != nil {
		t.Fatalf("future provider slug rejected: %v", err)
	}

	roles, known := KnownProviderRoles("mlflow")
	if !known || len(roles) == 0 {
		t.Fatal("mlflow missing from canonical provider-role matrix")
	}
	roles[0] = ExternalScheduler
	if known, supported := KnownProviderSupportsRole("mlflow", ExternalScheduler); !known || supported {
		t.Fatal("caller mutated canonical provider-role matrix")
	}
}

func TestAttemptArgvRejectsInvalidUTF8BeforePersistence(t *testing.T) {
	attempt := validPrivacyAttempt(t)
	attempt.Argv = []string{"runner", "--auth-token", "prefix\xffsecret-suffix"}
	err := Validate(attempt)
	if !hasIssueCode(err, "attempt.argv") || !hasIssueCode(err, "privacy.secret_argument") {
		t.Fatalf("invalid-UTF-8 secret argv validation = %v", err)
	}
	if strings.Contains(err.Error(), "secret-suffix") {
		t.Fatalf("invalid-UTF-8 argv diagnostic leaked a suffix: %v", err)
	}
}

func TestExternalRefMetadataUsesProviderNamespaceAndBoundedCommonTypes(t *testing.T) {
	attempt := validPrivacyAttempt(t)
	attempt.ExternalRefs[0].Metadata = map[string]any{
		"mlflow.experiment_id": int64(7),
		"mlflow.metrics":       []any{float64(0.5), "validation"},
		"com.example.mlflow":   map[string]any{"workspace": "synthetic"},
	}
	if err := Validate(attempt); err != nil {
		t.Fatalf("compatible ExternalRef metadata rejected: %v", err)
	}

	for name, metadata := range map[string]map[string]any{
		"unscoped":           {"experiment_id": int64(7)},
		"credential snake":   {"mlflow.auth_value": "CANARY"},
		"credential camel":   {"mlflow.databasePassword": "CANARY"},
		"credential session": {"mlflow.session_id": "CANARY"},
		"oversized":          {"mlflow.note": strings.Repeat("x", MaxExternalRefBytes)},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Clone(attempt).(*Attempt)
			candidate.ExternalRefs[0].Metadata = metadata
			if err := Validate(candidate); err == nil {
				t.Fatal("invalid ExternalRef metadata validated")
			}
		})
	}
}

func validPrivacyAttempt(t *testing.T) *Attempt {
	t.Helper()
	now := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	return &Attempt{
		Common:    Common{Schema: SchemaAttempt, ID: mustID(t, "att_01a01e69-b800-7505-8000-000000000505"), Title: "Attempt", CreatedAt: now, UpdatedAt: now},
		Run:       mustID(t, "run_01a01e68-cda0-7404-8000-000000000404"),
		State:     AttemptPlanned,
		Runner:    "direct",
		Scheduler: "direct",
		CWD:       ".",
		Argv:      []string{"python3", "train.py"},
		ExternalRefs: []ExternalRef{{
			Role: ExternalTracker, Provider: "mlflow", Context: "local", NativeKind: "run", NativeID: "00000000000000000000000000000001",
			URI: "https://mlflow.example.invalid/#/runs/00000000000000000000000000000001",
		}},
	}
}

func privacyProject(t *testing.T) *Project {
	t.Helper()
	projectID, err := ParseUUID("01a01e66-0e80-7101-8000-000000000101")
	if err != nil {
		t.Fatal(err)
	}
	return &Project{
		Schema: SchemaProject, ProjectID: projectID, Name: "Privacy test project",
		CreatedAt: time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC), ExperimentsRoot: ".",
	}
}

func privacyPlan(t *testing.T) *Plan {
	t.Helper()
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	return &Plan{
		Common:         Common{Schema: SchemaPlan, ID: mustID(t, "plan_01a01e66-f8e0-7202-8000-000000000202"), Title: "Compare tokenization methods", CreatedAt: now, UpdatedAt: now},
		Priority:       PriorityP1,
		Effort:         EffortS,
		State:          PlanQueued,
		ExpectedPayoff: ExpectedPayoff{Summary: "Measure whether the tokenizer improves accuracy", Metric: "macro_f1", Unit: "score"},
	}
}

func hasIssueCode(err error, code string) bool {
	return hasResearchIssue(err, code)
}

func hasResearchIssue(err error, code string) bool {
	for _, issue := range IssuesFromError(err) {
		if issue.Code == code {
			return true
		}
	}
	return false
}
