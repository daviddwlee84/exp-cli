package record

import (
	"errors"
	"testing"
)

func TestPrivacyPlanBodyRejectsCredentialMaterialWithoutRejectingScientificProse(t *testing.T) {
	unsafe := []string{
		"# Rationale\n\nSee https://alice:secret@example.invalid/run.\n",
		"# Rationale\n\nAuthorization: Bearer CANARY_PLAN_BODY_9d31\n",
		"# Rationale\n\nCookie: session=CANARY_PLAN_BODY_9d31\n",
		"# Rationale\n\n-----BEGIN PRIVATE KEY-----\nCANARY_PLAN_BODY_9d31\n-----END PRIVATE KEY-----\n",
	}
	for _, body := range unsafe {
		if err := validatePlanBody(body); !errors.Is(err, ErrInvalidBody) {
			t.Errorf("credential-bearing Plan body validated: %v", err)
		}
	}

	safe := "# Rationale\n\nStudy authorization and cookie policy as scientific controls. " +
		"See https://example.invalid/paper?section=results#figure-2.\n"
	if err := validatePlanBody(safe); err != nil {
		t.Fatalf("ordinary scientific Plan body rejected: %v", err)
	}
}
