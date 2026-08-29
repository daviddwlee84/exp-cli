package record

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSchemaLocalTemporalTypesFailIndependentlyOfProcessTimezone(t *testing.T) {
	for _, timezone := range []string{"UTC", "America/Los_Angeles"} {
		t.Run(strings.ReplaceAll(timezone, "/", "_"), func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestSchemaTemporalTimezoneSubprocess$")
			command.Env = append(os.Environ(), "EXP_SCHEMA_TEMPORAL_SUBPROCESS=1", "TZ="+timezone)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("local temporal validation under TZ=%s: %v\n%s", timezone, err, output)
			}
		})
	}
}

func TestSchemaTemporalTimezoneSubprocess(t *testing.T) {
	if os.Getenv("EXP_SCHEMA_TEMPORAL_SUBPROCESS") != "1" {
		return
	}
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
				t.Fatalf("Decode error = %v, want timestamp.type", err)
			}
		})
	}
}
