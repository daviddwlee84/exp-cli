package record

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaPreflightRejectsCaseVariantKeysAtEveryKnownLevel(t *testing.T) {
	plan := readSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	attempt := readSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")

	tests := []struct {
		name string
		data []byte
	}{
		{"lone schema variant", bytes.Replace(plan, []byte(`schema =`), []byte(`Schema =`), 1)},
		{"lone top-level variant", bytes.Replace(plan, []byte(`title =`), []byte(`Title =`), 1)},
		{"duplicate top-level variant", bytes.Replace(plan, []byte("title = \"Calibrate encoder learning rate\"\n"), []byte("title = \"Calibrate encoder learning rate\"\nTitle = \"Shadow title\"\n"), 1)},
		{"nested table variant", bytes.Replace(plan, []byte(`summary =`), []byte(`Summary =`), 1)},
		{"array-table variant", bytes.Replace(attempt, []byte(`native_id =`), []byte(`Native_ID =`), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var first string
			for iteration := 0; iteration < 25; iteration++ {
				_, err := Decode(test.data)
				var coded *Error
				if !errors.As(err, &coded) || coded.Code != "record.unknown_field" {
					t.Fatalf("case-variant key error = %v", err)
				}
				if iteration == 0 {
					first = err.Error()
				} else if err.Error() != first {
					t.Fatalf("nondeterministic error: %q != %q", err, first)
				}
			}
		})
	}
}

func TestSchemaPreflightPreservesOpenExtensionsAndExternalMetadata(t *testing.T) {
	plan := readSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	plan = bytes.Replace(plan, []byte("+++\n\n# Calibrate"), []byte("\n[extensions.\"org.example.review\"]\nCamelCase = { Arbitrary = [\"safe\"] }\nlocal_date = 2026-08-20\n+++\n\n# Calibrate"), 1)
	if _, err := Decode(plan); err != nil {
		t.Fatalf("open extension rejected: %v\n%s", err, plan)
	}

	attempt := readSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
	attempt = bytes.Replace(attempt, []byte("observed_at = 2026-08-20T09:42:00Z\n\n[provenance]"), []byte("observed_at = 2026-08-20T09:42:00Z\n\n[external_refs.metadata]\n\"mlflow.safe\" = { CamelCase = [\"safe\"] }\n\n[provenance]"), 1)
	if _, err := Decode(attempt); err != nil {
		t.Fatalf("open ExternalRef metadata rejected: %v\n%s", err, attempt)
	}
}

func TestSchemaPreflightRequiresPresentArraysAndEvidenceReason(t *testing.T) {
	experiment := readSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "REPORT.md")
	tests := []struct {
		name string
		data []byte
		code string
	}{
		{
			name: "secondary_factors omitted",
			data: bytes.Replace(experiment, []byte("secondary_factors = []\n"), nil, 1),
			code: "record.missing_field",
		},
		{
			name: "secondary_factors wrong type",
			data: bytes.Replace(experiment, []byte("secondary_factors = []"), []byte(`secondary_factors = "none"`), 1),
			code: "record.field_type",
		},
		{
			name: "included evidence reason omitted",
			data: bytes.Replace(experiment, []byte("reason = \"\"\n"), nil, 1),
			code: "record.missing_field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(test.data)
			var coded *Error
			if !errors.As(err, &coded) || coded.Code != test.code {
				t.Fatalf("Decode error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestSchemaPreflightRequiresActualOffsetDatetime(t *testing.T) {
	plan := readSchemaFixture(t, "plans", "plan_01a01e66-f8e0-7202-8000-000000000202-calibrate-encoder-learning-rate.md")
	for name, replacement := range map[string]string{
		"local date":     "2026-08-20",
		"local time":     "09:01:00",
		"local datetime": "2026-08-20T09:01:00",
	} {
		t.Run(name, func(t *testing.T) {
			data := bytes.Replace(plan, []byte("2026-08-20T09:01:00Z"), []byte(replacement), 1)
			_, err := Decode(data)
			var coded *Error
			if !errors.As(err, &coded) || coded.Code != "timestamp.type" {
				t.Fatalf("local temporal error = %v", err)
			}
		})
	}

	attempt := readSchemaFixture(t, "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
	nested := bytes.Replace(attempt, []byte("observed_at = 2026-08-20T09:42:00Z"), []byte("observed_at = 2026-08-20T09:42:00"), 1)
	_, err := Decode(nested)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "timestamp.type" || !strings.Contains(coded.Message, "external_refs[0].observed_at") {
		t.Fatalf("nested local datetime error = %v", err)
	}

	explicitUTC := bytes.Replace(plan, []byte("2026-08-20T09:01:00Z"), []byte("2026-08-20T09:01:00+00:00"), 1)
	if _, err := Decode(explicitUTC); err != nil {
		t.Fatalf("explicit UTC offset datetime rejected: %v", err)
	}
}

func readSchemaFixture(t *testing.T, components ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "testdata", "v1", "valid-project"}, components...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
