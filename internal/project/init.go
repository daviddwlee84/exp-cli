package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/gitx"
	"github.com/daviddwlee84/exp-cli/internal/lockx"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

var ErrProjectIdentityConflict = errors.New("linked worktrees contain conflicting Project identities")

// InitRequest is the side-effect-free input to project initialization.
type InitRequest struct {
	StartDir string
	Name     string
}

// InitOption injects deterministic creation seams.
type InitOption func(*initializer)

type initializer struct {
	clock             func() time.Time
	generate          research.UUIDGenerator
	git               gitx.Runner
	atomicHook        record.AtomicHook
	receiptHook       record.AtomicHook
	directorySyncHook func(string) error
}

func WithClock(clock func() time.Time) InitOption {
	return func(initializer *initializer) { initializer.clock = clock }
}

func WithUUIDGenerator(generator research.UUIDGenerator) InitOption {
	return func(initializer *initializer) { initializer.generate = generator }
}

func WithGitRunner(runner gitx.Runner) InitOption {
	return func(initializer *initializer) { initializer.git = runner }
}

func WithAtomicHook(hook record.AtomicHook) InitOption {
	return func(initializer *initializer) { initializer.atomicHook = hook }
}

// WithReceiptAtomicHook injects failures into the local Project receipt without
// changing the canonical PROJECT.md publication hook.
func WithReceiptAtomicHook(hook record.AtomicHook) InitOption {
	return func(initializer *initializer) { initializer.receiptHook = hook }
}

// WithDirectorySyncHook injects an error after a newly created canonical
// directory and its parent entry have been synced.
func WithDirectorySyncHook(hook func(string) error) InitOption {
	return func(initializer *initializer) { initializer.directorySyncHook = hook }
}

// Initialize creates or discovers the one v1 root. First initialization is
// serialized through the Git common directory. A private durable receipt makes
// the chosen Project identity stable across crashes and linked worktrees, while
// existing canonical Project records remain authoritative over that local state.
func Initialize(ctx context.Context, request InitRequest, options ...InitOption) (info *Info, created bool, err error) {
	service := &initializer{
		clock:    time.Now,
		generate: research.DefaultUUIDGenerator,
		git:      gitx.ExecRunner{},
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if service.clock == nil {
		service.clock = time.Now
	}
	if service.generate == nil {
		service.generate = research.DefaultUUIDGenerator
	}
	if service.git == nil {
		service.git = gitx.ExecRunner{}
	}
	repository, err := gitx.DiscoverWithRunner(ctx, request.StartDir, service.git)
	if err != nil {
		return nil, false, err
	}
	err = lockx.WithTrustedRoot(ctx, repository.GitCommonDir, "exp/v1", func(coordination *os.Root) error {
		if err := ensureCoordinationDirectories(coordination); err != nil {
			return err
		}
		if err := record.CheckTransactionArtifacts(coordination); err != nil {
			return err
		}
		projects, current, worktrees, err := linkedCanonicalProjects(ctx, repository, service.git)
		if err != nil {
			return err
		}
		verifyMissingWorktrees := func() error {
			return gitx.VerifyMissingWorktrees(worktrees)
		}
		canonical, err := selectCanonicalProject(projects, current)
		if err != nil {
			return err
		}
		root := filepath.Join(repository.Root, "experiments")
		receipt, receiptErr := readProjectReceipt(coordination)

		if current == nil {
			// Reading an existing receipt may identify abandoned writer temporaries,
			// but no new or repaired receipt is published until the default root has
			// been classified as safe to initialize.
			if (receipt != nil && receiptErr == nil) || canonical != nil {
				if _, statErr := os.Lstat(root); statErr == nil {
					existingRoot, openErr := pathx.OpenCanonicalRootNoSymlinks(root)
					if openErr != nil {
						return openErr
					}
					cleanupErr := record.CleanupAtomicTempsRoot(existingRoot)
					closeErr := existingRoot.Close()
					if cleanupErr != nil || closeErr != nil {
						return fmt.Errorf("clean abandoned initialization temporaries: %w", errors.Join(cleanupErr, closeErr))
					}
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return statErr
				}
			}
			classification, classifyErr := classifyUnmarkedRoot(root)
			if classifyErr != nil {
				return classifyErr
			}
			switch classification {
			case rootLegacy:
				return fmt.Errorf("%s: %w", root, ErrLegacyRoot)
			case rootUnrelated:
				return fmt.Errorf("%s: %w", root, ErrUnrelatedRoot)
			}
		}
		if receiptErr != nil && (canonical == nil || receipt == nil) {
			return receiptErr
		}

		var selectedDocument *record.Document
		var selectedContent []byte
		switch {
		case canonical != nil:
			selectedDocument = canonical.marker.Document.Clone()
			selectedContent = append([]byte(nil), canonical.marker.Content...)
			if receipt == nil || !bytes.Equal(receipt.content, selectedContent) {
				if err := writeProjectReceipt(coordination, selectedContent, receipt, lockBoundAtomicHook(coordination, service.receiptHook, verifyMissingWorktrees), verifyMissingWorktrees); err != nil {
					return fmt.Errorf("reconcile Project receipt from canonical Project: %w", err)
				}
			}
		case receipt != nil:
			if project, ok := receipt.document.Record.(*research.Project); ok && project.ProjectID.IsImported() {
				return errors.New("an imported Project receipt cannot initialize a worktree without its authenticated canonical archive")
			}
			selectedDocument = receipt.document.Clone()
			selectedContent = append([]byte(nil), receipt.content...)
		default:
			name := request.Name
			if name == "" {
				name = repository.Name
			}
			if err := validateProjectName(name); err != nil {
				return err
			}
			now := service.clock().UTC()
			generated, generateErr := service.generate(now)
			if generateErr != nil {
				return fmt.Errorf("generate Project UUIDv7: %w", generateErr)
			}
			projectID, idErr := research.NewProjectUUID(generated)
			if idErr != nil {
				return fmt.Errorf("generate Project UUIDv7: %w", idErr)
			}
			candidate := &record.Document{Record: &research.Project{
				Schema:          research.SchemaProject,
				ProjectID:       projectID,
				Name:            name,
				CreatedAt:       now,
				ExperimentsRoot: ".",
			}, Body: "\n# " + name + "\n", Path: record.ProjectFile}
			selectedContent, err = record.Encode(candidate)
			if err != nil {
				return err
			}
			if err := record.ValidateRecordSize(selectedContent); err != nil {
				return err
			}
			selectedDocument, err = record.Decode(selectedContent)
			if err != nil {
				return err
			}
			selectedDocument.Path = record.ProjectFile
			if err := writeProjectReceipt(coordination, selectedContent, nil, lockBoundAtomicHook(coordination, service.receiptHook, verifyMissingWorktrees), verifyMissingWorktrees); err != nil {
				return fmt.Errorf("publish Project receipt: %w", err)
			}
		}

		if current != nil {
			canonicalRoot, err := ensureCanonicalRootDirectories(root, service.directorySyncHook)
			if err != nil {
				return err
			}
			defer canonicalRoot.Close()
			rootInfo, err := canonicalRoot.Stat(".")
			if err != nil || !os.SameFile(current.marker.Identity, rootInfo) {
				return fmt.Errorf("canonical experiments root changed after discovery: %w", errors.Join(pathx.ErrOutsideRoot, err))
			}
			if err := record.CleanupAtomicTempsRoot(canonicalRoot); err != nil {
				return fmt.Errorf("clean abandoned initialization temporaries: %w", err)
			}
			if err := verifyMissingWorktrees(); err != nil {
				return err
			}
			info = &Info{Repository: repository, Root: root, Document: current.marker.Document}
			created = false
			return nil
		}

		canonicalRoot, err := ensureCanonicalRootDirectories(root, service.directorySyncHook)
		if err != nil {
			return err
		}
		defer canonicalRoot.Close()
		classification, classifyErr := classifyUnmarkedRoot(root)
		if classifyErr != nil || classification != rootAbsentOrEmpty {
			if classifyErr != nil {
				return classifyErr
			}
			return fmt.Errorf("%s changed during initialization: %w", root, ErrUnrelatedRoot)
		}
		if err := pathx.VerifyRootPath(root, canonicalRoot); err != nil {
			return fmt.Errorf("canonical root changed before Project publication: %w", err)
		}
		writeErr := record.AtomicWriteRoot(canonicalRoot, record.ProjectFile, selectedContent, record.AtomicWriteOptions{
			Hook: projectBoundAtomicHook(coordination, canonicalRoot, service.atomicHook, verifyMissingWorktrees),
			Verify: func() error {
				return errors.Join(pathx.VerifyRootPath(coordination.Name(), coordination), verifyMissingWorktrees())
			},
		})
		if writeErr != nil {
			var publication *record.PublicationError
			if errors.As(writeErr, &publication) && publication.Published {
				info = &Info{Repository: repository, Root: root, Document: selectedDocument}
				created = true
			}
			return writeErr
		}
		if err := verifyMissingWorktrees(); err != nil {
			return err
		}
		info = &Info{Repository: repository, Root: root, Document: selectedDocument}
		created = true
		return nil
	})
	return info, created, err
}

type linkedProject struct {
	worktree string
	marker   marker
}

func linkedCanonicalProjects(ctx context.Context, repository gitx.Repository, runner gitx.Runner) ([]linkedProject, *linkedProject, []gitx.Worktree, error) {
	worktrees, err := gitx.Worktrees(ctx, repository.Root, runner)
	if err != nil {
		return nil, nil, nil, err
	}
	foundCurrent := false
	for _, worktree := range worktrees {
		if !worktree.Missing && worktree.Root == repository.Root {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		worktrees = append(worktrees, gitx.Worktree{Root: repository.Root})
		sort.Slice(worktrees, func(left, right int) bool { return worktrees[left].Root < worktrees[right].Root })
	}
	var projects []linkedProject
	var current *linkedProject
	for _, worktree := range worktrees {
		if worktree.Missing {
			continue
		}
		found, ok, err := readDefaultMarker(ctx, worktree.Root)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("inspect linked worktree %q: %w", worktree.Root, err)
		}
		if !ok {
			continue
		}
		projects = append(projects, linkedProject{worktree: worktree.Root, marker: found})
		if worktree.Root == repository.Root {
			current = &projects[len(projects)-1]
		}
	}
	if err := gitx.VerifyMissingWorktrees(worktrees); err != nil {
		return nil, nil, nil, err
	}
	return projects, current, worktrees, nil
}

func selectCanonicalProject(projects []linkedProject, current *linkedProject) (*linkedProject, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	identities := make(map[string][]string)
	for index := range projects {
		identity := projectIdentity(projects[index].marker.Document)
		identities[identity] = append(identities[identity], projects[index].worktree)
	}
	if len(identities) != 1 {
		parts := make([]string, 0, len(identities))
		for identity, roots := range identities {
			sort.Strings(roots)
			parts = append(parts, identity+" in "+strings.Join(roots, ", "))
		}
		sort.Strings(parts)
		return nil, fmt.Errorf("%w: %s", ErrProjectIdentityConflict, strings.Join(parts, "; "))
	}
	if current != nil {
		return current, nil
	}
	return &projects[0], nil
}

func ensureCoordinationDirectories(coordination *os.Root) error {
	for _, name := range []string{"transactions", "attempts", "reservations"} {
		directory, _, err := pathx.EnsureRootAtNoSymlinks(coordination, name, 0o700)
		if err != nil {
			return fmt.Errorf("create coordination directory %s: %w", name, err)
		}
		opened, openErr := directory.Open(".")
		if openErr == nil {
			openErr = opened.Chmod(0o700)
			openErr = errors.Join(openErr, opened.Close())
		}
		closeErr := directory.Close()
		if openErr != nil || closeErr != nil {
			return errors.Join(openErr, closeErr)
		}
	}
	return nil
}

func ensureCanonicalRootDirectories(rootPath string, syncHook func(string) error) (*os.Root, error) {
	repositoryPath := filepath.Dir(rootPath)
	if filepath.Base(rootPath) != "experiments" {
		return nil, fmt.Errorf("canonical root must be the default experiments directory")
	}
	repository, err := pathx.OpenCanonicalRootNoSymlinks(repositoryPath)
	if err != nil {
		return nil, fmt.Errorf("open Git worktree for initialization: %w", err)
	}
	defer repository.Close()
	experiments, experimentsCreated, err := pathx.EnsureRootAtNoSymlinks(repository, "experiments", 0o755)
	if err != nil {
		return nil, fmt.Errorf("create experiments root: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = experiments.Close()
		}
	}()
	if experimentsCreated && syncHook != nil {
		if err := syncHook("experiments"); err != nil {
			return nil, fmt.Errorf("after syncing experiments root: %w", err)
		}
	}
	rootDirectory, err := experiments.Open(".")
	if err != nil {
		return nil, err
	}
	chmodErr := rootDirectory.Chmod(0o755)
	closeErr := rootDirectory.Close()
	if chmodErr != nil || closeErr != nil {
		return nil, errors.Join(chmodErr, closeErr)
	}
	for _, name := range record.CanonicalFlatDirs() {
		directory, directoryCreated, err := pathx.EnsureRootAtNoSymlinks(experiments, name, 0o755)
		if err != nil {
			return nil, fmt.Errorf("create canonical directory %s: %w", name, err)
		}
		if directoryCreated && syncHook != nil {
			if err := syncHook(name); err != nil {
				_ = directory.Close()
				return nil, fmt.Errorf("after syncing canonical directory %s: %w", name, err)
			}
		}
		opened, openErr := directory.Open(".")
		if openErr == nil {
			openErr = opened.Chmod(0o755)
			openErr = errors.Join(openErr, opened.Close())
		}
		closeErr := directory.Close()
		if openErr != nil || closeErr != nil {
			return nil, errors.Join(openErr, closeErr)
		}
	}
	if err := pathx.VerifyRootAt(repository, "experiments", experiments); err != nil {
		return nil, fmt.Errorf("experiments root changed during initialization: %w", err)
	}
	failed = false
	return experiments, nil
}

func validateProjectName(name string) error {
	if name != strings.TrimSpace(name) || name == "" || strings.ContainsAny(name, "\x00\r\n") {
		return errors.New("project name must be a non-empty, trimmed single line")
	}
	return nil
}

func lockBoundAtomicHook(root *os.Root, hook record.AtomicHook, checks ...func() error) record.AtomicHook {
	return func(stage record.AtomicStage, destination string) error {
		if hook != nil {
			if err := hook(stage, destination); err != nil {
				return err
			}
		}
		if err := pathx.VerifyRootPath(root.Name(), root); err != nil {
			return fmt.Errorf("coordination root changed during receipt publication: %w", err)
		}
		for _, check := range checks {
			if check != nil {
				if err := check(); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func projectBoundAtomicHook(coordination, canonical *os.Root, hook record.AtomicHook, checks ...func() error) record.AtomicHook {
	return func(stage record.AtomicStage, destination string) error {
		if hook != nil {
			if err := hook(stage, destination); err != nil {
				return err
			}
		}
		if err := pathx.VerifyRootPath(coordination.Name(), coordination); err != nil {
			return fmt.Errorf("coordination root changed during Project publication: %w", err)
		}
		if err := pathx.VerifyRootPath(canonical.Name(), canonical); err != nil {
			return fmt.Errorf("canonical root changed during Project publication: %w", err)
		}
		for _, check := range checks {
			if check != nil {
				if err := check(); err != nil {
					return err
				}
			}
		}
		return nil
	}
}
