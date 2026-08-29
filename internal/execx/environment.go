package execx

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// LookupEnv resolves one process-environment variable. It is injectable so
// callers can test environment policy without depending on the host process.
type LookupEnv func(string) (string, bool)

// Binding is one explicit child-process environment binding. Its value and
// source are deliberately private so formatting or JSON encoding a Binding
// cannot disclose a secret.
type Binding struct {
	name       string
	value      string
	source     string
	sensitive  bool
	fromSource bool
}

// Bind adds a non-secret literal environment value. Names that look
// credential-related are treated as sensitive even when Bind is used.
func Bind(name, value string) Binding {
	return Binding{name: name, value: value, sensitive: SensitiveName(name)}
}

// BindSecret adds a literal secret value. Prefer BindSecretFromEnv when the
// value is stored in the process environment so it is resolved only at invoke
// time.
func BindSecret(name, value string) Binding {
	return Binding{name: name, value: value, sensitive: true}
}

// BindSecretFromEnv resolves source at invoke time and binds it to name in the
// child. Neither the source value nor its name is exposed by plan rendering.
func BindSecretFromEnv(name, source string) Binding {
	return Binding{name: name, source: source, sensitive: true, fromSource: true}
}

// Name returns the child-process environment variable name.
func (b Binding) Name() string { return b.name }

// Sensitive reports whether the binding value must never be rendered.
func (b Binding) Sensitive() bool { return b.sensitive || SensitiveName(b.name) }

// String intentionally omits the value and secret source.
func (b Binding) String() string {
	if b.Sensitive() {
		return b.name + "=<sensitive>"
	}
	return b.name + "=<bound>"
}

// GoString intentionally omits the value and secret source.
func (b Binding) GoString() string { return b.String() }

// MarshalJSON emits metadata only, never a bound value or secret source.
func (b Binding) MarshalJSON() ([]byte, error) {
	return json.Marshal(EnvironmentVariable{Name: b.name, Sensitive: b.Sensitive()})
}

// EnvironmentVariable is safe plan metadata for one child-process variable.
type EnvironmentVariable struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive"`
}

// Environment is a deny-by-default child-process environment. Only explicitly
// allowed inherited names and explicit bindings are present in the child.
// Internal values are private to prevent accidental formatting or encoding.
type Environment struct {
	allowed  []string
	bindings []Binding
}

var minimalAllowlist = []string{
	"HOME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"PATH",
	"PATHEXT",
	"SYSTEMROOT",
	"TEMP",
	"TMP",
	"TMPDIR",
	"TZ",
	"WINDIR",
}

// MinimalAllowlist returns a copy of the portable baseline used by
// MinimalEnvironment. No credential-related variables are inherited by it.
func MinimalAllowlist() []string { return append([]string(nil), minimalAllowlist...) }

// NewEnvironment constructs a deny-by-default policy. allowed names are read
// from the parent only at invocation time; bindings override allowed values of
// the same name.
func NewEnvironment(allowed []string, bindings ...Binding) (Environment, error) {
	e := Environment{
		allowed:  append([]string(nil), allowed...),
		bindings: append([]Binding(nil), bindings...),
	}
	if err := e.validate(); err != nil {
		return Environment{}, err
	}
	sort.Strings(e.allowed)
	sort.Slice(e.bindings, func(i, j int) bool { return e.bindings[i].name < e.bindings[j].name })
	return e, nil
}

// MinimalEnvironment constructs an environment with only the portable
// baseline plus explicit bindings.
func MinimalEnvironment(bindings ...Binding) (Environment, error) {
	return NewEnvironment(MinimalAllowlist(), bindings...)
}

// Variables returns stable, value-free metadata suitable for operation plans.
func (e Environment) Variables() []EnvironmentVariable {
	byName := make(map[string]bool, len(e.allowed)+len(e.bindings))
	for _, name := range e.allowed {
		byName[name] = SensitiveName(name)
	}
	for _, binding := range e.bindings {
		byName[binding.name] = binding.Sensitive()
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]EnvironmentVariable, 0, len(names))
	for _, name := range names {
		out = append(out, EnvironmentVariable{Name: name, Sensitive: byName[name]})
	}
	return out
}

// MarshalJSON emits only variable names and sensitivity metadata.
func (e Environment) MarshalJSON() ([]byte, error) { return json.Marshal(e.Variables()) }

// String emits only stable metadata.
func (e Environment) String() string {
	parts := make([]string, 0, len(e.Variables()))
	for _, variable := range e.Variables() {
		if variable.Sensitive {
			parts = append(parts, variable.Name+"=<sensitive>")
		} else {
			parts = append(parts, variable.Name)
		}
	}
	return strings.Join(parts, ",")
}

// GoString emits only stable metadata.
func (e Environment) GoString() string { return e.String() }

func (e Environment) validate() error {
	seenAllowed := make(map[string]struct{}, len(e.allowed))
	for _, name := range e.allowed {
		if !validEnvironmentName(name) {
			return fmt.Errorf("invalid allowed environment variable name")
		}
		if _, exists := seenAllowed[name]; exists {
			return fmt.Errorf("duplicate allowed environment variable %q", name)
		}
		seenAllowed[name] = struct{}{}
	}

	seenBindings := make(map[string]struct{}, len(e.bindings))
	for _, binding := range e.bindings {
		if !validEnvironmentName(binding.name) {
			return fmt.Errorf("invalid bound environment variable name")
		}
		if _, exists := seenBindings[binding.name]; exists {
			return fmt.Errorf("duplicate bound environment variable %q", binding.name)
		}
		seenBindings[binding.name] = struct{}{}
		if binding.fromSource {
			if !validEnvironmentName(binding.source) {
				return fmt.Errorf("invalid secret environment source name")
			}
			if binding.value != "" {
				return fmt.Errorf("secret environment reference also contains a literal value")
			}
		} else if !utf8.ValidString(binding.value) {
			return fmt.Errorf("environment value is not valid UTF-8")
		} else if strings.IndexByte(binding.value, 0) >= 0 {
			return fmt.Errorf("environment value contains NUL")
		}
	}
	return nil
}

func (e Environment) knownSecretValues() []string {
	var values []string
	for _, binding := range e.bindings {
		if binding.Sensitive() && !binding.fromSource && binding.value != "" {
			values = append(values, binding.value)
		}
	}
	return values
}

func (e Environment) resolve(lookup LookupEnv) ([]string, []string, error) {
	if err := e.validate(); err != nil {
		return nil, nil, err
	}
	if lookup == nil {
		return nil, nil, fmt.Errorf("environment lookup is not configured")
	}

	values := make(map[string]string, len(e.allowed)+len(e.bindings))
	var secrets []string
	for _, name := range e.allowed {
		if value, ok := lookup(name); ok {
			if !utf8.ValidString(value) {
				return nil, nil, fmt.Errorf("allowed environment value is not valid UTF-8")
			}
			if strings.IndexByte(value, 0) >= 0 {
				return nil, nil, fmt.Errorf("allowed environment value contains NUL")
			}
			values[name] = value
			if SensitiveName(name) && value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	for _, binding := range e.bindings {
		value := binding.value
		if binding.fromSource {
			var ok bool
			value, ok = lookup(binding.source)
			if !ok {
				return nil, nil, fmt.Errorf("required secret environment source is not set")
			}
		}
		if !utf8.ValidString(value) {
			return nil, nil, fmt.Errorf("bound environment value is not valid UTF-8")
		}
		if strings.IndexByte(value, 0) >= 0 {
			return nil, nil, fmt.Errorf("bound environment value contains NUL")
		}
		values[binding.name] = value
		if binding.Sensitive() && value != "" {
			secrets = append(secrets, value)
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+values[name])
	}
	if entries == nil {
		entries = []string{}
	}
	return entries, secrets, nil
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index, r := range name {
		if index == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
