package workspacebackend

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
)

// BinaryLookup resolves one executable without running it.
type BinaryLookup func(string) (string, error)

// DevCLI reserves the optional dev-cli integration boundary. Invoker and Git
// remain injectable for source compatibility, but no public dev-cli release has
// a schema-versioned, content-free machine capability endpoint or exact-path
// open/retire receipt. No lifecycle subprocess is therefore authorized.
type DevCLI struct {
	Invoker          execx.Invoker
	LookupBinary     BinaryLookup
	Git              gitx.Runner
	ProbeTimeout     time.Duration
	OperationTimeout time.Duration
}

func unsupportedDevCapability(capability Capability) error {
	return &UnsupportedCapabilityError{Provider: DevCLIName, Capability: capability}
}

// Prepare deliberately performs no subprocess invocation or mutation.
func (DevCLI) Prepare(context.Context, PrepareRequest) (Workspace, error) {
	return Workspace{}, unsupportedDevCapability(CapabilityPrepare)
}

// Readiness performs executable discovery only. A requested live probe reports
// the compiled incompatibility without invoking human help/version output or an
// unrelated inventory endpoint; those surfaces cannot attest lifecycle safety.
func (adapter DevCLI) Readiness(ctx context.Context, _ string, probe bool) (Readiness, error) {
	status := Readiness{
		Provider:     DevCLIName,
		State:        ReadinessMissing,
		Capabilities: readinessCapabilities(ReadinessMissing),
	}
	if ctx == nil {
		return status, errors.New("workspace provider readiness requires a context")
	}
	if err := ctx.Err(); err != nil {
		status.State = ReadinessUnknown
		status.Reason = "probe-cancelled"
		status.Capabilities = readinessCapabilities(status.State)
		return status, err
	}
	binary, found, err := adapter.resolveBinary()
	if err != nil {
		status.State = ReadinessUnknown
		status.Reason = "invalid-binary-path"
		status.Capabilities = readinessCapabilities(status.State)
		return status, &ReadinessError{Readiness: status, Err: errors.Join(ErrProviderProbeFailed, err)}
	}
	if !found {
		status.Reason = "binary-not-found"
		return status, nil
	}
	status.BinaryFound = true
	status.binaryPath = binary
	status.State = ReadinessInstalledNotProbed
	status.Reason = "probe-not-requested"
	status.Capabilities = readinessCapabilities(status.State)
	if !probe {
		return status, nil
	}
	status.Probed = true
	status.State = ReadinessUnsupported
	status.Reason = "machine-contract-unavailable"
	status.Capabilities = readinessCapabilities(status.State)
	return status, &ReadinessError{Readiness: status, Err: ErrProviderIncompatible}
}

// Open remains unsupported until dev-cli accepts an exact canonical worktree
// path and returns a machine-verifiable runtime/open receipt.
func (DevCLI) Open(context.Context, Backend, PrepareRequest) (Inspection, error) {
	return Inspection{}, unsupportedDevCapability(CapabilityOpen)
}

// Handoff is an alias at the API boundary, not an authorization workaround.
func (DevCLI) Handoff(context.Context, Backend, PrepareRequest) (Inspection, error) {
	return Inspection{}, unsupportedDevCapability(CapabilityHandoff)
}

func (DevCLI) openWithReadiness(context.Context, Backend, PrepareRequest, Readiness) (Inspection, error) {
	return Inspection{}, unsupportedDevCapability(CapabilityHandoff)
}

// Retire remains native. Current dev-cli retirement can select task-owned state
// and lacks an exact unmanaged-path machine contract and verifiable occupancy
// result, so delegation is fail-closed.
func (DevCLI) Retire(context.Context, Backend, PrepareRequest) (Inspection, error) {
	return Inspection{}, unsupportedDevCapability(CapabilityRetire)
}

func (DevCLI) retireWithReadiness(context.Context, Backend, PrepareRequest, Readiness) (Inspection, error) {
	return Inspection{}, unsupportedDevCapability(CapabilityRetire)
}

func (adapter DevCLI) resolveBinary() (string, bool, error) {
	lookup := adapter.LookupBinary
	if lookup == nil {
		lookup = func(name string) (string, error) { return execLookPath(name) }
	}
	binary, err := lookup("dev")
	if err != nil || binary == "" {
		return "", false, nil
	}
	if !utf8.ValidString(binary) || strings.ContainsAny(binary, "\x00\r\n") {
		return "", false, errors.New("resolved binary path contains invalid text")
	}
	if !filepath.IsAbs(binary) {
		binary, err = filepath.Abs(binary)
		if err != nil {
			return "", false, errors.New("resolved binary path is not absolute")
		}
	}
	binary = filepath.Clean(binary)
	if !filepath.IsAbs(binary) {
		return "", false, errors.New("resolved binary path is not absolute")
	}
	return binary, true, nil
}

var execLookPath = exec.LookPath

func readinessStateError(status Readiness) error {
	var err error
	switch status.State {
	case ReadinessMissing:
		err = ErrProviderMissing
	case ReadinessMisconfigured:
		err = ErrProviderMisconfigured
	case ReadinessUnsupported:
		err = ErrProviderIncompatible
	case ReadinessUnknown:
		err = ErrProviderProbeFailed
	default:
		err = ErrProviderProbeFailed
	}
	return &ReadinessError{Readiness: status, Err: err}
}

func readinessCapabilities(state ReadinessState) []CapabilityStatus {
	descriptor := devCLIDescriptor()
	out := make([]CapabilityStatus, len(descriptor.Capabilities))
	for index, capability := range descriptor.Capabilities {
		out[index] = capability
		if capability.Support == SupportSupported && state != ReadinessReady {
			out[index].Support = SupportUnknown
		}
	}
	return out
}
