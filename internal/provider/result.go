package provider

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/safex"
)

// DiagnosticSeverity classifies a provider diagnostic without embedding an
// error whose text may be unsafe.
type DiagnosticSeverity string

const (
	SeverityInfo    DiagnosticSeverity = "info"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

// Diagnostic is bounded provider metadata. Unknown native tokens belong only
// in Native after sanitization.
type Diagnostic struct {
	Severity DiagnosticSeverity `json:"severity"`
	Code     string             `json:"code"`
	Message  string             `json:"message"`
	Native   string             `json:"native,omitempty"`
}

// NewDiagnostic constructs a safe bounded diagnostic.
func NewDiagnostic(severity DiagnosticSeverity, code, message, native string, policy RedactionPolicy) (Diagnostic, error) {
	diagnostic := Diagnostic{Severity: severity, Code: code, Message: message, Native: native}
	return sanitizeDiagnostic(diagnostic, policy)
}

// Validate checks the normalized diagnostic contract. Call NewDiagnostic when
// handling untrusted provider text.
func (d Diagnostic) Validate() error {
	if d.Severity != SeverityInfo && d.Severity != SeverityWarning && d.Severity != SeverityError {
		return fmt.Errorf("invalid diagnostic severity")
	}
	if !validDiagnosticCode(d.Code) {
		return fmt.Errorf("invalid diagnostic code")
	}
	if d.Message == "" || hasControl(d.Message) || len(d.Message) > DefaultMaxTextBytes {
		return fmt.Errorf("invalid diagnostic message")
	}
	if len(d.Native) > DefaultMaxTextBytes || hasControl(d.Native) {
		return fmt.Errorf("invalid diagnostic native value")
	}
	redactor := safex.NewRedactor()
	if redactor.Diagnostic(d.Message) != d.Message || redactor.Diagnostic(d.Native) != d.Native {
		return fmt.Errorf("diagnostic contains unsafe text")
	}
	return nil
}

func sanitizeDiagnostic(diagnostic Diagnostic, policy RedactionPolicy) (Diagnostic, error) {
	if diagnostic.Severity != SeverityInfo && diagnostic.Severity != SeverityWarning && diagnostic.Severity != SeverityError {
		diagnostic.Severity = SeverityWarning
	}
	if !validDiagnosticCode(diagnostic.Code) {
		diagnostic.Code = "provider_diagnostic"
	}
	message, err := sanitizeDiagnosticText(diagnostic.Message, policy)
	if err != nil {
		return Diagnostic{}, err
	}
	native, err := sanitizeDiagnosticText(diagnostic.Native, policy)
	if err != nil {
		return Diagnostic{}, err
	}
	diagnostic.Message = message
	diagnostic.Native = native
	if diagnostic.Message == "" {
		diagnostic.Message = "provider diagnostic"
	}
	return diagnostic, nil
}

func validDiagnosticCode(code string) bool {
	if code == "" {
		return false
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Severity != right.Severity {
			return left.Severity < right.Severity
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Message != right.Message {
			return left.Message < right.Message
		}
		return left.Native < right.Native
	})
}

// CapabilityResult is one stable capability-to-support probe result. An
// invalid native support token is retained only in NativeSupport while Support
// is unknown.
type CapabilityResult struct {
	Capability    Capability `json:"capability"`
	Support       Support    `json:"support"`
	NativeSupport string     `json:"native_support,omitempty"`
}

// ProbeResult is a local-only provider discovery result.
type ProbeResult struct {
	Provider           ProviderName       `json:"provider"`
	Context            ContextName        `json:"context"`
	ResolvedBinaryPath string             `json:"resolved_binary_path,omitempty"`
	ProviderVersion    string             `json:"provider_version,omitempty"`
	Capabilities       []CapabilityResult `json:"capabilities"`
	ObservedAt         time.Time          `json:"observed_at"`
	Diagnostics        []Diagnostic       `json:"diagnostics"`
}

// SupportFor returns a capability's support, failing closed when it is absent.
func (r ProbeResult) SupportFor(capability Capability) Support {
	for _, result := range r.Capabilities {
		if result.Capability == capability {
			return result.Support.Canonical()
		}
	}
	return SupportUnknown
}

// Validate verifies a probe against its compiled descriptor.
func (r ProbeResult) Validate(descriptor Descriptor) error {
	if r.Provider != descriptor.Name {
		return fmt.Errorf("probe provider does not match descriptor")
	}
	if !validSlug(string(r.Context)) {
		return fmt.Errorf("probe context is invalid")
	}
	if r.ResolvedBinaryPath != "" && (!filepath.IsAbs(r.ResolvedBinaryPath) || filepath.Clean(r.ResolvedBinaryPath) != r.ResolvedBinaryPath || hasControl(r.ResolvedBinaryPath)) {
		return fmt.Errorf("probe binary path is invalid")
	}
	if r.ObservedAt.IsZero() {
		return fmt.Errorf("probe observation time is required")
	}
	seen := make(map[Capability]struct{}, len(r.Capabilities))
	for _, capability := range r.Capabilities {
		if !descriptor.HasCapability(capability.Capability) {
			return fmt.Errorf("probe contains undeclared capability")
		}
		if _, duplicate := seen[capability.Capability]; duplicate {
			return fmt.Errorf("probe contains duplicate capability")
		}
		seen[capability.Capability] = struct{}{}
		if !capability.Support.Valid() {
			return fmt.Errorf("probe contains invalid normalized support")
		}
		if hasControl(capability.NativeSupport) {
			return fmt.Errorf("probe contains invalid native support")
		}
	}
	if len(seen) != len(descriptor.Capabilities) {
		return fmt.Errorf("probe omits a declared capability")
	}
	for _, diagnostic := range r.Diagnostics {
		if err := diagnostic.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// NormalizedState is an operational provider observation state. Unknown is the
// fail-closed value and never authorizes a retry or terminal transition.
type NormalizedState string

const (
	StatePlanned     NormalizedState = "planned"
	StateQueued      NormalizedState = "queued"
	StateBlocked     NormalizedState = "blocked"
	StateStarting    NormalizedState = "starting"
	StateRunning     NormalizedState = "running"
	StateSucceeded   NormalizedState = "succeeded"
	StateFailed      NormalizedState = "failed"
	StateCancelled   NormalizedState = "cancelled"
	StateTimedOut    NormalizedState = "timed_out"
	StatePreempted   NormalizedState = "preempted"
	StateOutOfMemory NormalizedState = "out_of_memory"
	StateUnknown     NormalizedState = "unknown"
)

var allStates = []NormalizedState{
	StatePlanned,
	StateQueued,
	StateBlocked,
	StateStarting,
	StateRunning,
	StateSucceeded,
	StateFailed,
	StateCancelled,
	StateTimedOut,
	StatePreempted,
	StateOutOfMemory,
	StateUnknown,
}

// AllNormalizedStates returns a stable copy of the closed state vocabulary.
func AllNormalizedStates() []NormalizedState { return append([]NormalizedState(nil), allStates...) }

// Valid reports whether state belongs to the normalized vocabulary.
func (s NormalizedState) Valid() bool {
	for _, known := range allStates {
		if s == known {
			return true
		}
	}
	return false
}

// Canonical fails closed to unknown.
func (s NormalizedState) Canonical() NormalizedState {
	if s.Valid() {
		return s
	}
	return StateUnknown
}

func (s NormalizedState) String() string { return string(s.Canonical()) }

func (s NormalizedState) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *NormalizedState) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = NormalizedState(value).Canonical()
	return nil
}

// Terminal reports only positively known terminal states. Unknown fails closed.
func (s NormalizedState) Terminal() bool {
	switch s.Canonical() {
	case StateSucceeded, StateFailed, StateCancelled, StateTimedOut, StatePreempted, StateOutOfMemory:
		return true
	default:
		return false
	}
}

// NormalizeNativeState applies an adapter-owned exact mapping. Unknown or
// invalid mapped values become unknown while the sanitized native token remains
// visible for diagnosis.
func NormalizeNativeState(native string, mapping map[string]NormalizedState, policy RedactionPolicy) (NormalizedState, string, *Diagnostic, error) {
	safeNative, err := sanitizeTextStrict(native, policy)
	if err != nil {
		return StateUnknown, "", nil, err
	}
	if state, found := mapping[native]; found && state.Valid() {
		return state, safeNative, nil, nil
	}
	diagnostic, err := NewDiagnostic(SeverityWarning, "unknown_native_state", "provider returned an unknown native state", safeNative, policy)
	if err != nil {
		return StateUnknown, "", nil, err
	}
	return StateUnknown, safeNative, &diagnostic, nil
}

// Observation is bounded, disposable provider state. Native and Raw fields are
// the only locations where unknown upstream values may remain visible.
type Observation struct {
	Provider         ProviderName    `json:"provider"`
	NativeProvider   string          `json:"native_provider,omitempty"`
	Context          ContextName     `json:"context"`
	NativeContext    string          `json:"native_context,omitempty"`
	ProviderVersion  string          `json:"provider_version,omitempty"`
	Capability       Capability      `json:"capability,omitempty"`
	NativeCapability string          `json:"native_capability,omitempty"`
	Support          Support         `json:"support"`
	NativeSupport    string          `json:"native_support,omitempty"`
	Source           string          `json:"source"`
	ObservedAt       time.Time       `json:"observed_at"`
	Stale            bool            `json:"stale"`
	Partial          bool            `json:"partial"`
	NormalizedState  NormalizedState `json:"normalized_state"`
	NativeState      string          `json:"native_state,omitempty"`
	NativeReason     string          `json:"native_reason,omitempty"`
	RawOnly          bool            `json:"raw_only"`
	RawState         any             `json:"raw_state,omitempty"`
	Diagnostics      []Diagnostic    `json:"diagnostics"`
}

// ObservationResult is a stable partial-success container for command-layer
// rendering. It carries no canonical scientific mutation.
type ObservationResult struct {
	Observations []Observation `json:"observations"`
	Partial      bool          `json:"partial"`
	ObservedAt   time.Time     `json:"observed_at"`
	Diagnostics  []Diagnostic  `json:"diagnostics"`
}

// OperationPlanSchema identifies reviewable provider plans.
const OperationPlanSchema = "exp.operation-plan/v1"

// OperationPlan is a value-free review of one explicit argument-array command.
type OperationPlan struct {
	Schema      string                      `json:"schema"`
	Provider    ProviderName                `json:"provider"`
	Context     ContextName                 `json:"context"`
	Role        Role                        `json:"role"`
	Capability  Capability                  `json:"capability"`
	Operation   string                      `json:"operation"`
	Executable  string                      `json:"executable"`
	Argv        []string                    `json:"argv"`
	CWD         string                      `json:"cwd"`
	Environment []execx.EnvironmentVariable `json:"environment"`
	Timeout     string                      `json:"timeout"`
	Output      execx.OutputView            `json:"output"`
	Effects     EffectSet                   `json:"effects"`
	Diagnostics []Diagnostic                `json:"diagnostics"`
	policy      RedactionPolicy
}

type operationPlanJSON OperationPlan

// OperationPlanRequest supplies typed provider metadata and an execx command.
type OperationPlanRequest struct {
	Provider    ProviderName
	Context     ContextName
	Role        Role
	Capability  Capability
	Operation   string
	Command     execx.CommandSpec
	Effects     EffectSet
	Diagnostics []Diagnostic
	Redaction   RedactionPolicy
}

// NewOperationPlan builds the only renderable form from CommandSpec.SafeView
// and applies provider redaction again at the adapter boundary.
func NewOperationPlan(request OperationPlanRequest) (OperationPlan, error) {
	policy, err := request.Redaction.effective()
	if err != nil {
		return OperationPlan{}, err
	}
	view, err := request.Command.SafeView()
	if err != nil {
		return OperationPlan{}, err
	}
	plan := OperationPlan{
		Schema:      OperationPlanSchema,
		Provider:    request.Provider,
		Context:     request.Context,
		Role:        request.Role,
		Capability:  request.Capability,
		Operation:   request.Operation,
		Environment: append([]execx.EnvironmentVariable(nil), view.Environment...),
		Timeout:     view.Timeout,
		Output:      view.Output,
		Effects:     request.Effects.normalized(),
		policy:      policy,
	}
	plan.Executable, err = sanitizeTextStrict(view.Executable, policy)
	if err != nil {
		return OperationPlan{}, err
	}
	plan.CWD, err = sanitizeTextStrict(view.CWD, policy)
	if err != nil {
		return OperationPlan{}, err
	}
	plan.Argv, err = SanitizeArgv(view.Argv, policy)
	if err != nil {
		return OperationPlan{}, err
	}
	plan.Diagnostics = make([]Diagnostic, 0, len(request.Diagnostics))
	for _, diagnostic := range request.Diagnostics {
		safe, err := sanitizeDiagnostic(diagnostic, policy)
		if err != nil {
			return OperationPlan{}, err
		}
		plan.Diagnostics = append(plan.Diagnostics, safe)
	}
	sortDiagnostics(plan.Diagnostics)
	if plan.Environment == nil {
		plan.Environment = []execx.EnvironmentVariable{}
	}
	if plan.Argv == nil {
		plan.Argv = []string{}
	}
	if plan.Diagnostics == nil {
		plan.Diagnostics = []Diagnostic{}
	}
	if err := plan.Validate(); err != nil {
		return OperationPlan{}, err
	}
	return plan, nil
}

// Validate enforces normalized provider/command/effect metadata and stable
// environment ordering. It contains no provider invocation.
func (p OperationPlan) Validate() error {
	if p.Schema != OperationPlanSchema {
		return fmt.Errorf("unsupported operation plan schema")
	}
	if !validSlug(string(p.Provider)) || p.Provider == ProviderUnknown {
		return fmt.Errorf("operation plan has invalid provider")
	}
	if !validSlug(string(p.Context)) {
		return fmt.Errorf("operation plan has invalid context")
	}
	if !p.Role.Valid() {
		return fmt.Errorf("operation plan has invalid role")
	}
	role, valid := p.Capability.Role()
	if !valid || role != p.Role {
		return fmt.Errorf("operation plan capability does not match role")
	}
	descriptor, compiled := CompiledRegistry().Get(p.Provider)
	if !compiled || !descriptor.HasRole(p.Role) || !descriptor.HasCapability(p.Capability) {
		return fmt.Errorf("operation plan capability is not compiled for provider")
	}
	if !validSlug(p.Operation) {
		return fmt.Errorf("operation plan has invalid operation")
	}
	if p.Executable == "" || p.CWD == "" || hasControl(p.Executable) || hasControl(p.CWD) ||
		!filepath.IsAbs(p.Executable) || filepath.Clean(p.Executable) != p.Executable ||
		!filepath.IsAbs(p.CWD) || filepath.Clean(p.CWD) != p.CWD {
		return fmt.Errorf("operation plan has invalid command paths")
	}
	if p.Timeout == "" {
		return fmt.Errorf("operation plan timeout metadata is required")
	}
	if p.Output.Mode != execx.OutputCapture && p.Output.Mode != execx.OutputStream {
		return fmt.Errorf("operation plan has invalid output mode")
	}
	if p.Output.MaxStdoutBytes <= 0 || p.Output.MaxStdoutBytes > execx.MaxOutputLimit || p.Output.MaxStderrBytes <= 0 || p.Output.MaxStderrBytes > execx.MaxOutputLimit {
		return fmt.Errorf("operation plan has invalid output bounds")
	}
	safeArgv := execx.NewRedactor().Argv(p.Argv)
	if !reflect.DeepEqual(safeArgv, p.Argv) {
		return fmt.Errorf("operation plan argv is not structurally redacted")
	}
	for _, argument := range p.Argv {
		if strings.ContainsRune(argument, 0) {
			return fmt.Errorf("operation plan argv contains NUL")
		}
	}
	previous := ""
	seenEnvironment := make(map[string]struct{}, len(p.Environment))
	for _, variable := range p.Environment {
		if variable.Name == "" || !validEnvironmentDisplayName(variable.Name) {
			return fmt.Errorf("operation plan environment name is invalid")
		}
		if _, duplicate := seenEnvironment[variable.Name]; duplicate {
			return fmt.Errorf("operation plan environment name is duplicated")
		}
		if previous != "" && variable.Name < previous {
			return fmt.Errorf("operation plan environment is not stably ordered")
		}
		seenEnvironment[variable.Name] = struct{}{}
		previous = variable.Name
	}
	if err := p.Effects.Validate(); err != nil {
		return err
	}
	if len(p.Effects.Values) == 0 {
		return fmt.Errorf("operation plan must declare at least one effect")
	}
	for _, diagnostic := range p.Diagnostics {
		if err := diagnostic.Validate(); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(operationPlanJSON(p))
	if err != nil || len(encoded) > execx.MaxCommandViewBytes {
		return fmt.Errorf("operation plan exceeds byte bound")
	}
	return nil
}

// MarshalJSON reapplies structural redaction so a manually assembled plan
// cannot serialize known credential argument forms.
func (p OperationPlan) MarshalJSON() ([]byte, error) {
	policy, err := p.policy.effective()
	if err != nil {
		return nil, err
	}
	safe := p
	safe.policy = policy
	safe.Effects = p.Effects.normalized()
	safe.Environment = append([]execx.EnvironmentVariable(nil), p.Environment...)
	sort.Slice(safe.Environment, func(i, j int) bool { return safe.Environment[i].Name < safe.Environment[j].Name })
	if safe.Environment == nil {
		safe.Environment = []execx.EnvironmentVariable{}
	}
	if safe.Argv == nil {
		safe.Argv = []string{}
	}
	safe.Executable, err = sanitizeTextStrict(p.Executable, policy)
	if err != nil {
		return nil, err
	}
	safe.CWD, err = sanitizeTextStrict(p.CWD, policy)
	if err != nil {
		return nil, err
	}
	safe.Argv, err = SanitizeArgv(p.Argv, policy)
	if err != nil {
		return nil, err
	}
	safe.Diagnostics = make([]Diagnostic, 0, len(p.Diagnostics))
	for _, diagnostic := range p.Diagnostics {
		diagnostic, err = sanitizeDiagnostic(diagnostic, policy)
		if err != nil {
			return nil, err
		}
		safe.Diagnostics = append(safe.Diagnostics, diagnostic)
	}
	sortDiagnostics(safe.Diagnostics)
	if err := safe.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(operationPlanJSON(safe))
}

func (p OperationPlan) String() string {
	encoded, err := p.MarshalJSON()
	if err != nil {
		return "<invalid-operation-plan>"
	}
	return string(encoded)
}

func (p OperationPlan) GoString() string { return p.String() }
