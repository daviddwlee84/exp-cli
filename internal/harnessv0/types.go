// Package harnessv0 implements the explicit, lossless migration path from the
// unversioned experiment-knowledge-harness layout. It never executes legacy
// scripts and never runs automatically.
package harnessv0

import "time"

const (
	PlanSchema       = "exp.migration-plan.harness-v0/v1"
	ResolutionSchema = "exp.migration-resolutions.harness-v0/v1"
	ManifestSchema   = "exp.migration-archive.harness-v0/v1"
)

type Span struct {
	Kind       string `json:"kind" toml:"kind"`
	StartByte  int64  `json:"start_byte" toml:"start_byte"`
	EndByte    int64  `json:"end_byte" toml:"end_byte"`
	SHA256     string `json:"sha256" toml:"sha256"`
	MappingKey string `json:"mapping_key,omitempty" toml:"mapping_key,omitempty"`
}

type SourceFile struct {
	Path   string `json:"path" toml:"path"`
	Bytes  int64  `json:"bytes" toml:"bytes"`
	SHA256 string `json:"sha256" toml:"sha256"`
	Spans  []Span `json:"spans" toml:"spans"`
}

type Mapping struct {
	Key              string   `json:"key"`
	Kind             string   `json:"kind"`
	SourcePath       string   `json:"source_path"`
	StartByte        int64    `json:"start_byte"`
	EndByte          int64    `json:"end_byte"`
	StableSourceKey  string   `json:"stable_source_key"`
	ID               string   `json:"id,omitempty"`
	Destination      string   `json:"destination,omitempty"`
	CandidateSHA256  string   `json:"candidate_sha256,omitempty"`
	Status           string   `json:"status"`
	ReviewKeys       []string `json:"review_keys"`
	CandidateContent string   `json:"candidate_content_base64,omitempty"`
}

type Diagnostic struct {
	Key       string `json:"key,omitempty"`
	State     string `json:"state"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Path      string `json:"path,omitempty"`
	StartByte int64  `json:"start_byte,omitempty"`
	EndByte   int64  `json:"end_byte,omitempty"`
}

type Resolution struct {
	Key    string `json:"key" toml:"key"`
	Action string `json:"action" toml:"action"`
	Note   string `json:"note,omitempty" toml:"note,omitempty"`
}

type ResolutionSet struct {
	SchemaVersion string       `json:"schema_version"`
	Resolutions   []Resolution `json:"resolutions"`
}

type CandidateFile struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
	Generated     bool   `json:"generated"`
}

type Archive struct {
	Path                  string `json:"path"`
	ManifestSHA256        string `json:"manifest_sha256"`
	ManifestContentBase64 string `json:"manifest_content_base64"`
}

type Plan struct {
	SchemaVersion       string          `json:"schema_version"`
	ReaderVersion       string          `json:"reader_version"`
	TargetSchemaVersion string          `json:"target_schema_version"`
	GeneratedAt         time.Time       `json:"generated_at"`
	SourceRoot          string          `json:"source_root"`
	TreeFingerprint     string          `json:"tree_fingerprint"`
	ProjectID           string          `json:"project_id"`
	ContentHash         string          `json:"content_hash"`
	Applicable          bool            `json:"applicable"`
	SourceFiles         []SourceFile    `json:"source_files"`
	Mappings            []Mapping       `json:"mappings"`
	UnknownSpans        []SpanReference `json:"unknown_spans"`
	Diagnostics         []Diagnostic    `json:"diagnostics"`
	Resolutions         []Resolution    `json:"resolutions"`
	Archive             Archive         `json:"archive"`
	CandidateFiles      []CandidateFile `json:"candidate_files"`
	UnifiedDiff         string          `json:"unified_diff"`
}

type SpanReference struct {
	Path      string `json:"path"`
	StartByte int64  `json:"start_byte"`
	EndByte   int64  `json:"end_byte"`
	SHA256    string `json:"sha256"`
}

type ApplyResult struct {
	Applied         bool     `json:"applied"`
	AlreadyApplied  bool     `json:"already_applied"`
	PlanHash        string   `json:"plan_hash"`
	TreeFingerprint string   `json:"tree_fingerprint"`
	ArchivePath     string   `json:"archive_path"`
	Published       []string `json:"published"`
	TransactionID   string   `json:"transaction_id"`
}

type manifest struct {
	Schema          string       `toml:"schema"`
	ReaderVersion   string       `toml:"reader_version"`
	TreeFingerprint string       `toml:"tree_fingerprint"`
	SourceRoot      string       `toml:"source_root"`
	Files           []SourceFile `toml:"files"`
	Resolutions     []Resolution `toml:"resolutions,omitempty"`
}
