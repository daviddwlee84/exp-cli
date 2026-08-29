package record

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestUnicodeBodyAndExtensionsRoundTripLosslessly(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 8, 7, 123_000_000, time.FixedZone("UTC+0", 0))
	plan := validTestPlan(t, now)
	plan.Title = "学習率 café 🚀"
	plan.Tags = []string{"unicode", "encoder"}
	plan.Extensions = research.Extensions{
		"org.example.review": {
			"reviewed_by": "合成レビュー担当",
			"nested":      map[string]any{"score": int64(7), "accepted": true},
		},
	}
	body := "\n# 学習率 café 🚀\n\nLiteral delimiter in Markdown:\n\n```text\n+++\n```\n"
	document := &Document{Record: plan, Body: body, Path: "ignored/original.md"}
	encoded, err := Encode(document)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if bytes.Contains(encoded, []byte("+00:00")) || !bytes.Contains(encoded, []byte("2026-08-29T09:08:07.123Z")) {
		t.Fatalf("timestamp was not normalized to Z:\n%s", encoded)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v\n%s", err, encoded)
	}
	if decoded.Body != body {
		t.Fatalf("body changed\n got: %q\nwant: %q", decoded.Body, body)
	}
	got := decoded.Record.(*research.Plan)
	if got.Title != plan.Title || got.Extensions["org.example.review"]["reviewed_by"] != "合成レビュー担当" {
		t.Fatalf("Unicode metadata changed: %#v", got)
	}
	reencoded, err := Encode(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("normalized re-encode differs: %v\n%s\n%s", err, encoded, reencoded)
	}
}

func TestRevisionNormalizesSetsAndIgnoresPath(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	first := &Document{Record: validTestPlan(t, now), Body: "body", Path: "one.md"}
	second := first.Clone()
	first.Record.(*research.Plan).Tags = []string{"zeta", "alpha"}
	second.Record.(*research.Plan).Tags = []string{"alpha", "zeta"}
	second.Path = "another/location.md"
	left, err := Revision(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Revision(second)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("normalized revisions differ: %s != %s", left, right)
	}
	second.Body = "body changed"
	changed, err := Revision(second)
	if err != nil {
		t.Fatal(err)
	}
	if changed == left {
		t.Fatal("body change did not change revision")
	}
}

func TestStrictEnvelopeAndNestedUnknownFields(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "v1", "valid-project", "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(data, []byte("exit_code = 0\n"), []byte("exit_code = 0\nunknown_nested = true\n"), 1)
	_, err = Decode(unknown)
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "record.unknown_field" || !strings.Contains(coded.Message, "terminal.unknown_nested") {
		t.Fatalf("nested unknown error = %v", err)
	}
	duplicate := bytes.Replace(data, []byte("runner = \"direct\"\n"), []byte("runner = \"direct\"\nrunner = \"again\"\n"), 1)
	if _, err := Decode(duplicate); err == nil {
		t.Fatal("duplicate TOML key validated")
	}
	for name, malformed := range map[string][]byte{
		"leading byte": append([]byte(" "), data...),
		"CRLF":         bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")),
		"missing LF":   bytes.TrimSuffix(data, []byte("\n")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(malformed); err == nil {
				t.Fatal("malformed envelope validated")
			}
		})
	}
}

func TestExternalReferenceMetadataIsNamespacedAndRecursivelyOpen(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "v1", "valid-project", "e-01a01e67-calibrate-encoder-learning-rate", "attempts", "att_01a01e69-b800-7505-8000-000000000505.md")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	withMetadata := bytes.Replace(data,
		[]byte("observed_at = 2026-08-20T09:42:00Z\n\n[provenance]"),
		[]byte("observed_at = 2026-08-20T09:42:00Z\n\n[external_refs.metadata]\n\"com.example.mlflow\" = { nested = { safe = \"value\" } }\n\n[provenance]"), 1)
	document, err := Decode(withMetadata)
	if err != nil {
		t.Fatalf("decode namespaced metadata: %v\n%s", err, withMetadata)
	}
	attempt := document.Record.(*research.Attempt)
	if len(attempt.ExternalRefs) != 1 || attempt.ExternalRefs[0].Metadata["com.example.mlflow"] == nil {
		t.Fatalf("metadata was not preserved: %#v", attempt.ExternalRefs)
	}
	unnamespaced := bytes.Replace(withMetadata, []byte("\"com.example.mlflow\""), []byte("unsafe"), 1)
	if _, err := Decode(unnamespaced); err == nil {
		t.Fatal("unnamespaced ExternalRef metadata validated")
	}
}

func TestKnownNestedFieldsRejectNonUTCAndExtensionsRemainOpen(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	plan := validTestPlan(t, now)
	plan.Extensions = research.Extensions{"com.example.tool": {"arbitrary": map[string]any{"key": "value"}}}
	encoded, err := Encode(&Document{Record: plan, Body: "body\n"})
	if err != nil {
		t.Fatalf("open extension: %v", err)
	}
	nonUTC := bytes.Replace(encoded, []byte("2026-08-29T09:00:00Z"), []byte("2026-08-29T17:00:00+08:00"), 1)
	if _, err := Decode(nonUTC); err == nil {
		t.Fatal("non-UTC core timestamp validated")
	}
}

func validTestPlan(t *testing.T, now time.Time) *research.Plan {
	t.Helper()
	id, err := research.ParseID("plan_01a01e66-f8e0-7202-8000-000000000202")
	if err != nil {
		t.Fatal(err)
	}
	return &research.Plan{
		Common:         research.Common{Schema: research.SchemaPlan, ID: id, Title: "Plan", CreatedAt: now, UpdatedAt: now, Tags: []string{"encoder"}},
		Priority:       research.PriorityP1,
		Effort:         research.EffortS,
		State:          research.PlanQueued,
		ExpectedPayoff: research.ExpectedPayoff{Summary: "Improve score", Metric: "macro_f1", Unit: "score"},
	}
}
