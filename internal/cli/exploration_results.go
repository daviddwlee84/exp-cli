package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/execx"
	"github.com/daviddwlee84/exp-cli/internal/exploration"
	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/tryflow"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

type artifactView struct {
	Attempt      string                      `json:"attempt"`
	Try          string                      `json:"try,omitempty"`
	Artifact     exploration.Artifact        `json:"artifact"`
	Availability string                      `json:"availability"`
	Runner       *exploration.RunnerIdentity `json:"runner,omitempty"`
}

func resultAttempts(inventory *record.Inventory, reference string) ([]*record.Document, error) {
	document, err := resolveCanonicalDocument(inventory, reference)
	if err != nil {
		return nil, err
	}
	if document.Kind() == research.KindAttempt {
		return []*record.Document{document}, nil
	}
	if document.Kind() != research.KindTry {
		return nil, errors.New("results require a Try or Attempt reference")
	}
	id, _ := document.ID()
	result := []*record.Document{}
	for _, attempt := range inventory.OfKind(research.KindAttempt) {
		if attempt.Record.(*research.Attempt).Try == id {
			result = append(result, attempt)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Record.GetCommon().CreatedAt.Before(result[j].Record.GetCommon().CreatedAt)
	})
	return result, nil
}

func newResultsCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "results", Short: "Save, locate, compare, and retrieve artifacts by exact execution identity"}
	list := explorationLeaf("list TRY|ATTEMPT", "List artifact identities and local availability without contacting providers", app, func(cmd *cobra.Command, args []string) (any, error) { return listResultViews(cmd, app, root, args) })
	list.Args = cobra.ExactArgs(1)
	compare := explorationLeaf("compare TRY|ATTEMPT TRY|ATTEMPT...", "Compare artifact names, content identities, and runner versions", app, func(cmd *cobra.Command, args []string) (any, error) { return listResultViews(cmd, app, root, args) })
	compare.Args = cobra.MinimumNArgs(2)
	save := explorationLeaf("save TRY|ATTEMPT", "Retry artifact publication without re-executing a workload", app, func(cmd *cobra.Command, args []string) (any, error) {
		info, store, err := openRecoveryTransactionalStore(cmd, app, root)
		if err != nil {
			return nil, err
		}
		inventory, err := store.Inventory(cmd.Context())
		if err != nil {
			return nil, err
		}
		attempts, err := resultAttempts(inventory, args[0])
		if err != nil {
			return nil, err
		}
		allow, _ := cmd.Flags().GetBool("allow-large")
		result := []canonicalRecordView{}
		var failures []error
		for _, document := range attempts {
			id, _ := document.ID()
			saved, err := tryflow.SaveArtifacts(cmd.Context(), store, info.Project().ProjectID.String(), id, allow)
			if saved != nil {
				result = append(result, canonicalView(saved))
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
		if len(result) > 0 && len(failures) > 0 {
			return result, &explorationMutationError{errors.Join(failures...)}
		}
		return result, errors.Join(failures...)
	})
	save.Args = cobra.ExactArgs(1)
	save.Flags().Bool("allow-large", false, "approve transfers above the selected storage profile threshold")
	for _, action := range []string{"fetch", "open"} {
		action := action
		leaf := explorationLeaf(action+" TRY|ATTEMPT NAME", "Retrieve an exact named artifact and verify its recorded content digest", app, func(cmd *cobra.Command, args []string) (any, error) {
			info, store, err := openRecoveryTransactionalStore(cmd, app, root)
			if err != nil {
				return nil, err
			}
			inventory, err := store.Inventory(cmd.Context())
			if err != nil {
				return nil, err
			}
			attempts, err := resultAttempts(inventory, args[0])
			if err != nil {
				return nil, err
			}
			var matches []artifactView
			for _, document := range attempts {
				attempt := document.Record.(*research.Attempt)
				metadata, err := exploration.MetadataFor(attempt)
				if err != nil {
					return nil, err
				}
				if metadata == nil {
					continue
				}
				for _, artifact := range metadata.Artifacts {
					if artifact.Name == args[1] {
						matches = append(matches, artifactView{Attempt: attempt.ID.String(), Try: attempt.Try.String(), Artifact: artifact})
					}
				}
			}
			if len(matches) != 1 {
				return map[string]any{"matches": matches}, fmt.Errorf("expected one artifact, found %d; select its exact Attempt from results list", len(matches))
			}
			match := matches[0]
			execution, err := exploration.LoadExecution(cmd.Context(), info.Project().ProjectID.String(), match.Attempt)
			if err != nil {
				// A cloned Project can retrieve remote artifacts using its locally
				// configured profile without reconstructing the original host paths.
				settings, loadErr := exploration.Load(cmd.Context(), "")
				if loadErr != nil {
					return nil, loadErr
				}
				profile, ok := settings.Storage[match.Artifact.Storage]
				if !ok || profile.Kind == "local" {
					return nil, err
				}
				execution = exploration.Execution{Project: info.Project().ProjectID.String(), Try: match.Try, Attempt: match.Attempt, StorageName: match.Artifact.Storage, Storage: profile}
			}
			object, err := exploration.Fetch(cmd.Context(), execution, match.Artifact)
			if err != nil {
				return nil, err
			}
			destination, _ := cmd.Flags().GetString("destination")
			if destination == "" {
				cache, err := localstate.CacheHome()
				if err != nil {
					return nil, err
				}
				destination = filepath.Join(cache, "exp", "results", execution.Project, execution.Attempt, filepath.FromSlash(match.Artifact.Name))
			} else {
				destination, err = filepath.Abs(destination)
				if err != nil {
					return nil, err
				}
			}
			if err := exploration.CopyVerified(cmd.Context(), object, destination, match.Artifact); err != nil {
				return nil, err
			}
			data := map[string]any{"attempt": match.Attempt, "artifact": match.Artifact, "path": destination}
			if action == "open" {
				binary := "xdg-open"
				argv := []string{destination}
				if runtime.GOOS == "darwin" {
					binary = "open"
				} else if runtime.GOOS == "windows" {
					binary = "rundll32.exe"
					argv = []string{"url.dll,FileProtocolHandler", destination}
				}
				resolved, err := exec.LookPath(binary)
				if err != nil {
					return data, errors.New("no desktop opener is available; use the returned path")
				}
				resolved, err = filepath.Abs(resolved)
				if err != nil {
					return data, err
				}
				environment, err := execx.NewEnvironment(append(execx.MinimalAllowlist(), "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"))
				if err != nil {
					return data, err
				}
				_, err = app.Invoker.Invoke(cmd.Context(), execx.CommandSpec{Executable: resolved, Argv: argv, CWD: filepath.Dir(destination), Environment: environment, Timeout: 10 * time.Second, Output: execx.DefaultOutputPolicy(execx.OutputCapture)})
				if err != nil {
					return data, errors.New("desktop opener failed; use the returned path")
				}
			}
			return data, nil
		})
		leaf.Args = cobra.ExactArgs(2)
		leaf.Flags().String("destination", "", "write to this local file; default is a named verified cache file")
		command.AddCommand(leaf)
	}
	describe := explorationLeaf("describe ATTEMPT NAME --description TEXT", "Add a searchable description to one saved artifact", app, func(cmd *cobra.Command, args []string) (any, error) {
		_, store, err := openRecoveryTransactionalStore(cmd, app, root)
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
		if document.Kind() != research.KindAttempt {
			return nil, errors.New("describe requires an exact Attempt")
		}
		id, _ := document.ID()
		text, _ := cmd.Flags().GetString("description")
		updated, err := tryflow.DescribeArtifact(cmd.Context(), store, id, args[1], text)
		if err != nil {
			return nil, err
		}
		return canonicalView(updated), nil
	})
	describe.Args = cobra.ExactArgs(2)
	describe.Flags().String("description", "", "brief searchable description of the plot, table, or file")
	command.AddCommand(list, compare, save, describe)
	return command
}

func listResultViews(cmd *cobra.Command, app *App, root *rootOptions, references []string) (any, error) {
	info, store, err := openRecoveryTransactionalStore(cmd, app, root)
	if err != nil {
		return nil, err
	}
	inventory, err := store.Inventory(cmd.Context())
	if err != nil {
		return nil, err
	}
	result := []artifactView{}
	for _, reference := range references {
		attempts, err := resultAttempts(inventory, reference)
		if err != nil {
			return nil, err
		}
		for _, document := range attempts {
			attempt := document.Record.(*research.Attempt)
			metadata, err := exploration.MetadataFor(attempt)
			if err != nil {
				return nil, err
			}
			if metadata == nil {
				continue
			}
			execution, loadErr := exploration.LoadExecution(cmd.Context(), info.Project().ProjectID.String(), attempt.ID.String())
			var local *exploration.Execution
			if loadErr == nil {
				local = &execution
			}
			for _, artifact := range metadata.Artifacts {
				result = append(result, artifactView{Attempt: attempt.ID.String(), Try: attempt.Try.String(), Artifact: artifact, Availability: exploration.Availability(local, artifact), Runner: metadata.Runner})
			}
		}
	}
	return result, nil
}

func newHistoryCommand(app *App, root *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "history", Short: "Retrieve experiments and explorations using descriptions, versions, and artifacts"}
	search := explorationLeaf("search [WORDS...]", "Search canonical content using a rebuildable local SQLite index", app, func(cmd *cobra.Command, args []string) (any, error) {
		all, _ := cmd.Flags().GetBool("all")
		limit, _ := cmd.Flags().GetInt("limit")
		kind, _ := cmd.Flags().GetString("kind")
		state, _ := cmd.Flags().GetString("state")
		version, _ := cmd.Flags().GetString("version")
		source, _ := cmd.Flags().GetString("source-key")
		query := exploration.HistoryQuery{Text: strings.Join(args, " "), Limit: limit, Kind: kind, State: state, Version: version, Source: source}
		for _, bound := range []struct {
			name   string
			target *time.Time
		}{{"after", &query.After}, {"before", &query.Before}} {
			text, _ := cmd.Flags().GetString(bound.name)
			if text != "" {
				value, err := time.Parse(time.RFC3339, text)
				if err != nil {
					value, err = time.Parse("2006-01-02", text)
				}
				if err != nil {
					return nil, fmt.Errorf("--%s requires an ISO date or timestamp", bound.name)
				}
				*bound.target = value
			}
		}
		projects := map[string][]exploration.Card{}
		skipped := []string{}
		if all {
			associations, err := app.Associations.List(cmd.Context())
			if err != nil {
				return nil, err
			}
			for _, association := range associations.Projects {
				resolved, err := app.ResolveWorkspace.Resolve(cmd.Context(), workspace.ResolveRequest{InvocationDir: association.Root, Workspace: association.ProjectID.String(), SkipConfig: true})
				if err != nil {
					skipped = append(skipped, association.ProjectID.String())
					continue
				}
				store, err := app.NewTransactionalStore(resolved.Project)
				if err != nil {
					return nil, err
				}
				inventory, err := store.Inventory(cmd.Context())
				if err != nil || !inventory.Valid() {
					skipped = append(skipped, association.ProjectID.String())
					continue
				}
				projects[association.ProjectID.String()] = exploration.Cards(association.ProjectID.String(), inventory)
			}
		} else {
			info, store, err := openRecoveryTransactionalStore(cmd, app, root)
			if err != nil {
				return nil, err
			}
			inventory, err := store.Inventory(cmd.Context())
			if err != nil {
				return nil, err
			}
			if !inventory.Valid() {
				return nil, errors.New("cannot search an invalid canonical inventory")
			}
			projects[info.Project().ProjectID.String()] = exploration.Cards(info.Project().ProjectID.String(), inventory)
		}
		cards, err := exploration.Search(cmd.Context(), projects, query)
		return map[string]any{"results": cards, "skipped_projects": skipped, "partial": len(skipped) > 0}, err
	})
	search.Args = cobra.ArbitraryArgs
	search.Flags().Bool("all", false, "search all registered Projects, including adhoc collections")
	search.Flags().Int("limit", 50, "maximum matching records, 1..1000")
	search.Flags().String("kind", "", "filter canonical record kind")
	search.Flags().String("state", "", "filter lifecycle or execution state")
	search.Flags().String("version", "", "filter Source commit prefix, runner version, or binary digest")
	search.Flags().String("source-key", "", "filter Source key or full Source ID without selecting an execution checkout")
	search.Flags().String("after", "", "inclusive ISO date or timestamp")
	search.Flags().String("before", "", "exclusive ISO date or timestamp")
	command.AddCommand(search)
	return command
}
