package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/project"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	sourcepkg "github.com/daviddwlee84/exp-cli/internal/source"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

func newTryStartCommand(app *App, root *rootOptions) *cobra.Command {
	command := explorationLeaf("start --title TITLE --goal GOAL [--scratch]", "Start an exploration before running its first step", app, func(cmd *cobra.Command, _ []string) (any, error) {
		title, _ := cmd.Flags().GetString("title")
		goal, _ := cmd.Flags().GetString("goal")
		scratch, _ := cmd.Flags().GetBool("scratch")
		tags, _ := cmd.Flags().GetStringSlice("tags")
		if scratch && (root.workspace != "" || root.source != "") {
			return nil, errors.New("--scratch selects the dedicated adhoc collection; omit --workspace/--source or start in the selected workspace without --scratch")
		}
		if strings.TrimSpace(title) == "" || strings.TrimSpace(goal) == "" {
			return nil, errors.New("try start requires --title and --goal")
		}
		if err := research.ValidateCommitSafeText(title + "\n" + goal); err != nil {
			return nil, err
		}
		selection, err := trySelection(app, root)
		if err != nil {
			return nil, err
		}
		var resolved *workspace.Context
		if !scratch {
			resolved, err = app.ResolveWorkspace.Resolve(cmd.Context(), workspace.ResolveRequest{InvocationDir: selection.InvocationDir, Workspace: selection.Workspace, Source: selection.Source})
			if err != nil {
				_, gitErr := gitx.DiscoverWithRunner(cmd.Context(), selection.InvocationDir, app.GitRunner)
				if root.workspace == "" && root.source == "" && errors.Is(gitErr, gitx.ErrNotRepository) {
					scratch = true
				} else {
					return nil, err
				}
			}
		}
		if scratch {
			resolved, err = createScratchSource(cmd.Context(), app, title)
			if err != nil {
				return nil, err
			}
		}
		if resolved.Source == nil {
			store, err := app.NewTransactionalStore(resolved.Project)
			if err != nil {
				return nil, err
			}
			inventory, err := store.Inventory(cmd.Context())
			if err != nil {
				return nil, err
			}
			active := []*research.Source{}
			for _, doc := range inventory.OfKind(research.KindSource) {
				v := doc.Record.(*research.Source)
				if v.State == research.SourceActive {
					active = append(active, v)
				}
			}
			if len(active) != 1 {
				return nil, errors.New("select an execution Source with --source")
			}
			resolved, err = app.ResolveWorkspace.Resolve(cmd.Context(), workspace.ResolveRequest{InvocationDir: selection.InvocationDir, Workspace: resolved.Project.Root, Source: active[0].ID.String()})
			if err != nil {
				return nil, err
			}
		}
		store, err := app.NewTransactionalStore(resolved.Project)
		if err != nil {
			return nil, err
		}
		inventory, err := store.Inventory(cmd.Context())
		if err != nil {
			return nil, err
		}
		source, err := inventory.ByID(resolved.Source.ID)
		if err != nil {
			return nil, err
		}
		result, err := tryflow.New(store, tryflow.WithClock(app.Now), tryflow.WithUUIDGenerator(app.GenerateUUID)).Create(cmd.Context(), tryflow.CreateRequest{Title: title, Goal: goal, Tags: tags,
			Sources: []tryflow.RevisionRef{{ID: resolved.Source.ID, Revision: source.Revision}}, Extensions: research.Extensions{exploration.Namespace: {"schema": "exp.exploration-session/v1", "scratch": scratch}}})
		if err != nil {
			if scratch {
				return map[string]any{"project": resolved.ProjectID().String(), "source": resolved.Source.ID.String(), "source_path": resolved.ResolvedRoot}, &explorationMutationError{err}
			}
			return nil, err
		}
		id, _ := result.Try.ID()
		return map[string]any{"try": canonicalView(result.Try), "project": resolved.ProjectID().String(), "source": resolved.Source.ID.String(), "source_path": resolved.ResolvedRoot, "scratch": scratch,
			"next": "exp --workspace " + resolved.ProjectID().String() + " try exec " + id.String() + " -- COMMAND [ARG...]"}, nil
	})
	command.Args = cobra.NoArgs
	command.Flags().String("title", "", "short retrievable exploration title")
	command.Flags().String("goal", "", "question the exploration should answer")
	command.Flags().Bool("scratch", false, "create a small isolated Git Source under the configured tries root")
	command.Flags().StringSlice("tags", nil, "searchable exploration tags")
	return command
}

func createScratchSource(ctx context.Context, app *App, title string) (*workspace.Context, error) {
	settings, err := exploration.Load(ctx, "")
	if err != nil {
		return nil, err
	}
	canonical := filepath.Join(settings.TriesRoot, "records")
	var info *project.Info
	// Initialization of the collection is serialized independently of each
	// Source. A second start creates another Source, never shares a writable one.
	err = localstate.WithLockedFile(ctx, filepath.Join(settings.TriesRoot, "collection.json"), func(root *os.Root, name string) error {
		previous, err := localstate.ReadRoot(ctx, root, name, 4096)
		if err != nil {
			return err
		}
		type collectionReceipt struct {
			Schema  string `json:"schema_version"`
			State   string `json:"state"`
			Project string `json:"project,omitempty"`
		}
		receipt := collectionReceipt{Schema: "exp.scratch-collection/v1", State: "preparing"}
		if previous == nil {
			if _, err := root.Lstat("records"); err == nil {
				return errors.New("unregistered records directory already exists under tries_root; choose an empty collection root")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			data, _ := json.Marshal(receipt)
			if err := localstate.WriteRoot(root, name, data, nil, nil); err != nil {
				return err
			}
			previous, err = localstate.ReadRoot(ctx, root, name, 4096)
			if err != nil {
				return err
			}
		} else if err := exploration.Decode(previous.Data, &receipt); err != nil || receipt.Schema != "exp.scratch-collection/v1" || (receipt.State != "preparing" && receipt.State != "ready") {
			return errors.New("invalid scratch collection receipt")
		}
		if receipt.State == "preparing" {
			if err := root.Mkdir("records", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			entry, err := root.Lstat("records")
			if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
				return errors.New("scratch records directory is not a real directory")
			}
			if err := scratchGit(ctx, app, canonical, "init"); err != nil {
				return err
			}
		}
		info, _, err = app.InitializeProject(ctx, project.InitRequest{StartDir: canonical, Name: "Adhoc explorations"})
		if err != nil {
			return err
		}
		if receipt.State == "ready" && receipt.Project != info.Project().ProjectID.String() {
			return errors.New("adhoc collection Project identity changed")
		}
		if _, err := app.Associations.RegisterProject(ctx, info); err != nil {
			return err
		}
		if receipt.State == "preparing" {
			receipt.State, receipt.Project = "ready", info.Project().ProjectID.String()
			data, _ := json.Marshal(receipt)
			return localstate.WriteRoot(root, name, data, previous, nil)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	id, err := app.GenerateUUID(app.Now())
	if err != nil {
		return nil, err
	}
	key := "scratch-" + id.String()
	dir := filepath.Join(settings.TriesRoot, "sources", key)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	if err := scratchGit(ctx, app, dir, "init"); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+title+"\n\nKeep analysis code here. Read EXP_INPUT_NAME bindings and write artifacts to EXP_OUTPUT_DIR.\n"), 0o600); err != nil {
		return nil, err
	}
	if err := scratchGit(ctx, app, dir, "add", "--", "README.md"); err != nil {
		return nil, err
	}
	if err := scratchGit(ctx, app, dir, "-c", "user.name=exp", "-c", "user.email=exp@localhost", "-c", "commit.gpgsign=false", "commit", "--no-verify", "-m", "Initialize isolated exploration source"); err != nil {
		return nil, err
	}
	repo, err := gitx.DiscoverWithRunner(ctx, dir, app.GitRunner)
	if err != nil {
		return nil, err
	}
	plan, err := sourcepkg.BuildAddPlan(sourcepkg.AddPlanRequest{Request: sourcepkg.AddRequest{Key: key, Title: title, Subdir: "."}, CloneRoot: repo.Root, CloneGitCommonDir: repo.GitCommonDir, ConfirmLocalOnly: true})
	if err != nil {
		return nil, err
	}
	store, err := app.NewTransactionalStore(info)
	if err != nil {
		return nil, err
	}
	source, err := app.NewSourceService(store).ApplyAddPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	sourceID, _ := source.ID()
	if _, err := app.Associations.RegisterSource(ctx, info.Project().ProjectID, sourceID, dir); err != nil {
		return nil, err
	}
	return app.ResolveWorkspace.Resolve(ctx, workspace.ResolveRequest{InvocationDir: dir, Workspace: info.Root, Source: sourceID.String()})
}

func scratchGit(ctx context.Context, app *App, dir string, args ...string) error {
	_, _, err := app.GitRunner.Run(ctx, dir, append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...))
	if err != nil {
		return fmt.Errorf("scratch Git operation %s failed: %w", args[0], err)
	}
	return nil
}

func newTryExecCommand(app *App, root *rootOptions) *cobra.Command {
	options := &tryRunOptions{}
	command := &cobra.Command{Use: "exec TRY -- [COMMAND [ARG...]]", Short: "Append a new exploration step with independent outputs and provenance", Args: cobra.MinimumNArgs(1)}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 1 {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, errors.New("try exec requires TRY followed by -- and argv"))
		}
		selection, err := trySelection(app, root)
		if err != nil {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, err)
		}
		info, _, err := openTransactionalStore(cmd, app, root)
		if err != nil {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, err)
		}
		argv, runner, err := runnerArgv(cmd.Context(), info.Project().ProjectID.String(), options.runner, args[1:])
		if err != nil {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, err)
		}
		if len(argv) == 0 {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, errors.New("provide COMMAND or configure a runner profile"))
		}
		if options.dirty != "" && options.dirty != "capture" {
			return commandFailure(app, options.json, "try exec", emptyTryExecutionData(), false, nil, errors.New("--dirty accepts only capture"))
		}
		result, err := app.NewTryCoordinator().Run(cmd.Context(), tryflow.RunRequest{Selection: selection, ExistingTry: args[0], Title: options.title, Argv: argv,
			ManagedOutputs: true, StorageProfile: options.storage, RunnerProfile: runner, InputNames: options.inputs, AllowLarge: options.allowLarge,
			DirtyCapture: options.dirty == "capture", AllowedGlobs: options.allow, Timeout: options.timeout})
		return renderTryExecution(app, options.json, "try exec", result, err)
	}
	command.Flags().BoolVar(&options.json, "json", false, jsonFlagUsage)
	command.Flags().StringVar(&options.title, "title", "", "optional title for this execution step")
	command.Flags().StringVar(&options.dirty, "dirty", "", "capture bounded uncommitted analysis code for this Try only")
	command.Flags().StringArrayVar(&options.allow, "allow", nil, "allow changes to matching Source-relative code paths")
	command.Flags().DurationVar(&options.timeout, "timeout", 0, "bound execution time")
	addExplorationFlags(command, options)
	return command
}

func newTrySummarizeCommand(app *App, root *rootOptions) *cobra.Command {
	command := explorationLeaf("summarize TRY --summary TEXT", "Save an attributed working summary without asserting human review", app, func(cmd *cobra.Command, args []string) (any, error) {
		summary, _ := cmd.Flags().GetString("summary")
		author, _ := cmd.Flags().GetString("author")
		if strings.TrimSpace(summary) == "" || len(summary) > research.MaxTryConclusionSummaryBytes || (author != "agent" && author != "human") {
			return nil, errors.New("summary is required and author must be agent or human")
		}
		info, store, err := openTransactionalStore(cmd, app, root)
		if err != nil {
			return nil, err
		}
		inventory, err := store.Inventory(cmd.Context())
		if err != nil {
			return nil, err
		}
		document, err := resolveCanonicalDocument(inventory, args[0])
		if err != nil {
			return nil, err
		}
		value, ok := document.Record.(*research.Try)
		if !ok {
			return nil, errors.New("summary owner must be a Try")
		}
		replacement := document.Clone()
		updated := replacement.Record.(*research.Try)
		if updated.Extensions == nil {
			updated.Extensions = research.Extensions{}
		}
		table := updated.Extensions[exploration.Namespace]
		if table == nil {
			table = map[string]any{"schema": "exp.exploration-session/v1"}
			updated.Extensions[exploration.Namespace] = table
		}
		table["summary"], table["summary_author"], table["summary_at"], table["reviewed"] = summary, author, app.Now().UTC().Format(time.RFC3339Nano), author == "human"
		result, err := store.Transact(cmd.Context(), record.TransactionRequest{Operation: "try.summarize", Changes: []record.TransactionChange{{Operation: record.TransactionReplace, Document: replacement, ExpectedRevision: document.Revision}}})
		if err != nil {
			return nil, err
		}
		_ = refreshAfterTransaction(cmd, app, info, store)
		return map[string]any{"try": value.ID.String(), "summary": summary, "author": author, "reviewed": author == "human", "transaction_id": result.TransactionID}, nil
	})
	command.Args = cobra.ExactArgs(1)
	command.Flags().String("summary", "", "brief description of observations and result locations")
	command.Flags().String("author", "agent", "agent or human; does not change formal evidence status")
	return command
}
