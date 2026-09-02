# Workflows

The workflow guides connect `exp` commands to the research decisions they
represent. Start with the core loop, then open the specialized guide for the
boundary you are operating.

| Guide | Use it when |
|---|---|
| [Core research workflow](core-workflow.md) | Moving from an Idea to a qualified, queued, evaluated result |
| [Agents and workspaces](agents-and-workspaces.md) | Asking a CLI agent to plan or implement isolated changes |
| [Runtime dispatch](runtime-dispatch.md) | Binding a Plan to Pueue, Git, argv, outputs, and capacity |
| [Evidence to promotion](evidence-to-promotion.md) | Closing an Experiment or creating a Candidate, Release, and Promotion |
| [Harness-v0 migration](migration.md) | Importing an older research harness through an explicit plan/apply flow |

!!! warning "Authority does not move with convenience"
    A generated projection, provider dashboard, or agent recommendation can
    inform a command, but it never becomes canonical simply because it is easy
    to read. Use the domain command and review the resulting records.

Run `exp validate` after supported mutations and `exp render --check` when a
workflow depends on generated project views being current.
