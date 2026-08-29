package research

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommitSafeTextRejectsAdversarialCredentialAndControlForms(t *testing.T) {
	unsafe := []string{
		"clientSecret=CANARY",
		"Run with --token CANARY",
		"Authorization Bearer CANARY",
		"See https://example.invalid/?access%255Ftoken=CANARY",
		"safe\x1b]52;c;Q0FOQVJZ\a",
	}
	for _, value := range unsafe {
		if err := ValidateCommitSafeText(value); !errors.Is(err, ErrUnsafeText) {
			t.Errorf("adversarial text %q validated: %v", value, err)
		}
	}

	for _, value := range []string{
		"Token: count used by the tokenizer",
		"Use token=[CLS] for the classification representation.",
		"Run with --max-tokens 2048 and compare token counts.",
	} {
		if err := ValidateCommitSafeText(value); err != nil {
			t.Errorf("ordinary NLP prose %q rejected: %v", value, err)
		}
	}
}

func TestCommittedRunAndAttemptPathsRejectCredentialMaterial(t *testing.T) {
	run := &Run{
		Common:     privacyCommonForPaths(t, SchemaRun, "run_01a01e68-cda0-7404-8000-000000000404", "Run"),
		Experiment: mustID(t, "exp_01a01e67-e340-7303-8000-000000000303"),
		Role:       RunBatch, Objective: "Compare arms", ExpectedOutputs: []string{"artifacts/access_token=CANARY"},
	}
	if err := Validate(run); !hasIssueCode(err, "privacy.secret") {
		t.Fatalf("credential-bearing expected output validated: %v", err)
	}
	run.ExpectedOutputs = []string{"../access_token=PATH_CANARY"}
	if err := Validate(run); err == nil || strings.Contains(err.Error(), "PATH_CANARY") {
		t.Fatalf("invalid path diagnostic leaked credential value: %v", err)
	}

	attempt := validPrivacyAttempt(t)
	attempt.CWD = "password=CANARY"
	if err := Validate(attempt); !hasIssueCode(err, "privacy.secret") {
		t.Fatalf("credential-bearing cwd validated: %v", err)
	}

	run.ExpectedOutputs = []string{"artifacts/token-count.json"}
	if err := Validate(run); err != nil {
		t.Fatalf("ordinary output path rejected: %v", err)
	}
	attempt = validPrivacyAttempt(t)
	attempt.CWD = "work/tokenizer-cache"
	if err := Validate(attempt); err != nil {
		t.Fatalf("ordinary scientific cwd rejected: %v", err)
	}
}

func privacyCommonForPaths(t *testing.T, schema Schema, id, title string) Common {
	t.Helper()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return Common{Schema: schema, ID: mustID(t, id), Title: title, CreatedAt: now, UpdatedAt: now}
}
