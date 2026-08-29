<!-- generated from exp command metadata; do not edit -->

# Current `exp` commands

This reference contains only the command metadata supplied by this build's CLI layer. It is not a roadmap for deferred commands.

## `exp`

Use the Git-native research control plane.

```text
exp
```

## `exp context`

Show a local, resumable research summary without provider refresh.

```text
exp context [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp doctor`

Inspect local core and optional-tool capabilities.

```text
exp doctor [--json] [--live]
```

Options:

- `--json` — emit the versioned machine-readable envelope
- `--live` — permit only the explicitly documented live probes

## `exp init`

Initialize an idempotent v1 experiments root.

```text
exp init
```

## `exp plan`

Work with priced research Plans.

```text
exp plan
```

## `exp plan add`

Create one validated Plan from human flags or versioned JSON input.

```text
exp plan add [flags | --input -] [--json]
```

Options:

- `--input` — read the versioned Plan request from standard input (must be -)
- `--json` — emit the versioned machine-readable envelope

## `exp plan list`

List canonical Plans without contacting providers.

```text
exp plan list [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope

## `exp render`

Render deterministic projections or check them without writing.

```text
exp render [--check]
```

Options:

- `--check` — report projection drift without writing

## `exp skill`

Inspect or manage the version-matched embedded guidance skill.

```text
exp skill print|install|check
```

## `exp skill check`

Check installed files, compatibility, manifest hash, and consumer links without mutation.

```text
exp skill check
```

## `exp skill install`

Atomically install the embedded skill and safe consumer links.

```text
exp skill install
```

## `exp skill print`

Print this build's embedded SKILL.md.

```text
exp skill print
```

## `exp validate`

Validate canonical local records without provider calls.

```text
exp validate [--json]
```

Options:

- `--json` — emit the versioned machine-readable envelope
