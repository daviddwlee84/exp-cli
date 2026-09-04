package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/agentcli"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
	"github.com/daviddwlee84/exp-cli/internal/workspace"
	"github.com/spf13/cobra"
)

const noFileCompletion = cobra.ShellCompDirectiveNoFileComp

func completionProtocolInvocation(command *cobra.Command) bool {
	if command == nil {
		return false
	}
	return command.Name() == cobra.ShellCompRequestCmd || command.CalledAs() == cobra.ShellCompNoDescRequestCmd
}

type completionDiagnosticWriter struct {
	destination io.Writer
}

func (writer completionDiagnosticWriter) Write(content []byte) (int, error) {
	if strings.HasPrefix(string(content), "Completion ended with directive: ShellCompDirective") {
		return len(content), nil
	}
	if writer.destination == nil {
		return len(content), nil
	}
	return writer.destination.Write(content)
}

type positionalCompletionSpec struct {
	kinds      [][]research.Kind
	allRecords bool
	sourceRefs bool
}

func installCompletions(root *cobra.Command, app *App, options *rootOptions) {
	commands := commandTreeByPath(root)
	registerFlagCompletion(root, "color", fixedCompletions(colorAuto, colorAlways, colorNever))
	registerFlagCompletion(root, "workspace", completeWorkspaces(app))
	registerFlagCompletion(root, "source", completeRecords(app, options, true, true, research.KindSource))
	registerFlagCompletion(root, "workspace-backend", completeWorkspaceBackends(app))
	registerFlagCompletion(root, "mlflow-profile", completeMLflowProfiles(app, options))

	positionals := map[string]positionalCompletionSpec{
		"exp record show":                  {allRecords: true},
		"exp source show":                  {sourceRefs: true},
		"exp source status":                {sourceRefs: true},
		"exp source register":              {sourceRefs: true},
		"exp source append-locator":        {sourceRefs: true},
		"exp source retire":                {sourceRefs: true},
		"exp idea qualify":                 {kinds: [][]research.Kind{{research.KindIdea}}},
		"exp idea develop":                 {kinds: [][]research.Kind{{research.KindIdea}}},
		"exp plan refresh":                 {kinds: [][]research.Kind{{research.KindPlan}}},
		"exp queue show":                   {kinds: [][]research.Kind{{research.KindQueue}}},
		"exp queue insert":                 {kinds: [][]research.Kind{{research.KindQueue}, {research.KindPlan}}},
		"exp queue remove":                 {kinds: [][]research.Kind{{research.KindQueue}, {research.KindPlan}}},
		"exp experiment agent":             {kinds: [][]research.Kind{{research.KindExperiment}}},
		"exp experiment workspace prepare": {kinds: [][]research.Kind{{research.KindExperiment}}},
		"exp experiment workspace commit":  {kinds: [][]research.Kind{{research.KindExperiment}}},
		"exp try retry":                    {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try resume":                   {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try reconcile":                {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try finish":                   {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try abandon":                  {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try adopt":                    {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try show":                     {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try status":                   {kinds: [][]research.Kind{{research.KindTry}}},
		"exp try cleanup":                  {kinds: [][]research.Kind{{research.KindTry}}},
		"exp workspace backend handoff":    {kinds: [][]research.Kind{{research.KindTry}}},
	}
	for path, specification := range positionals {
		command := commands[path]
		if command == nil {
			panic("completion references missing command " + path)
		}
		command.ValidArgsFunction = completePositionals(app, options, specification)
	}

	registerCommandFlagCompletion(commands, "exp record list", "kind", fixedCompletions(recordKindNames()...))
	registerCommandFlagCompletion(commands, "exp evaluation spec create", "pool", completeRecords(app, options, false, false, research.KindResourcePool))
	registerCommandFlagCompletion(commands, "exp evaluation create", "spec", completeRecords(app, options, false, false, research.KindEvaluationSpec))
	registerCommandFlagCompletion(commands, "exp evaluation create", "subject", completeRecords(app, options, false, false, research.KindExperiment, research.KindCandidate, research.KindRelease))
	registerCommandFlagCompletion(commands, "exp evaluation create", "attempt", completeRecords(app, options, false, false, research.KindAttempt))
	registerCommandFlagCompletion(commands, "exp candidate create", "experiment", completeRecords(app, options, false, false, research.KindExperiment))
	registerCommandFlagCompletion(commands, "exp candidate create", "evaluation", completeRecords(app, options, false, false, research.KindEvaluation))
	registerCommandFlagCompletion(commands, "exp candidate create", "attempt", completeRecords(app, options, false, false, research.KindAttempt))
	registerCommandFlagCompletion(commands, "exp promotion spec-create", "target", completeTargets(app, options))
	registerCommandFlagCompletion(commands, "exp promotion spec-create", "evaluation-spec", completeRecords(app, options, false, false, research.KindEvaluationSpec))
	registerCommandFlagCompletion(commands, "exp promotion append", "target", completeTargets(app, options))
	registerCommandFlagCompletion(commands, "exp promotion append", "spec", completeRecords(app, options, false, false, research.KindPromotionSpec))
	registerCommandFlagCompletion(commands, "exp promotion append", "challenger", completeRecords(app, options, false, false, research.KindRelease))
	registerCommandFlagCompletion(commands, "exp promotion append", "evaluation", completeRecords(app, options, false, false, research.KindEvaluation))
	registerCommandFlagCompletion(commands, "exp champion manifest", "target", completeTargets(app, options))
	registerCommandFlagCompletion(commands, "exp queue create", "pool", completeRecords(app, options, false, false, research.KindResourcePool))
	registerCommandFlagCompletion(commands, "exp queue insert", "pool", completeRecords(app, options, false, false, research.KindResourcePool))
	registerCommandFlagCompletion(commands, "exp idea qualify", "resource", completeResourceNeeds(app, options))
	registerCommandFlagCompletion(commands, "exp workspace backend handoff", "attempt", completeRecords(app, options, false, false, research.KindAttempt))

	for _, target := range []struct {
		path string
		flag string
	}{
		{"exp agent run", "profile"},
		{"exp idea develop", "profile"},
		{"exp experiment agent", "profile"},
		{"exp queue insert", "advisor-profile"},
		{"exp queue insert", "battle-profile"},
	} {
		registerCommandFlagCompletion(commands, target.path, target.flag, completeAgentProfiles(app))
	}
	registerCommandFlagCompletion(commands, "exp agent run", "role", completeAgentRoles(app))

	if guide := commands["exp guide"]; guide != nil {
		guide.ValidArgsFunction = fixedCompletions(guideTopicNames()...)
	}
}

func commandTreeByPath(root *cobra.Command) map[string]*cobra.Command {
	commands := map[string]*cobra.Command{}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		commands[command.CommandPath()] = command
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
	return commands
}

func registerCommandFlagCompletion(commands map[string]*cobra.Command, path, flag string, completion cobra.CompletionFunc) {
	command := commands[path]
	if command == nil {
		panic("completion references missing command " + path)
	}
	registerFlagCompletion(command, flag, completion)
}

func registerFlagCompletion(command *cobra.Command, name string, completion cobra.CompletionFunc) {
	if err := command.RegisterFlagCompletionFunc(name, completion); err != nil {
		panic(err)
	}
}

func fixedCompletions(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		builder := newCompletionBuilder(toComplete)
		for _, value := range values {
			builder.add(value, "")
		}
		return builder.values(), noFileCompletion
	}
}

func completePositionals(app *App, options *rootOptions, specification positionalCompletionSpec) cobra.CompletionFunc {
	return func(command *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if specification.allRecords {
			if len(args) > 0 {
				return nil, noFileCompletion
			}
			return recordCompletionValues(command, app, options, false, false, nil, toComplete, true)
		}
		if specification.sourceRefs {
			if len(args) > 0 {
				return nil, noFileCompletion
			}
			return recordCompletionValues(command, app, options, false, true, []research.Kind{research.KindSource}, toComplete, false)
		}
		if len(args) >= len(specification.kinds) {
			return nil, noFileCompletion
		}
		return recordCompletionValues(command, app, options, false, false, specification.kinds[len(args)], toComplete, false)
	}
}

func completeRecords(app *App, options *rootOptions, omitSelectedSource, sourceReferences bool, kinds ...research.Kind) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return recordCompletionValues(command, app, options, omitSelectedSource, sourceReferences, kinds, toComplete, false)
	}
}

func recordCompletionValues(command *cobra.Command, app *App, options *rootOptions, omitSelectedSource, sourceReferences bool, kinds []research.Kind, toComplete string, includeSpecial bool) ([]string, cobra.ShellCompDirective) {
	inventory, ok := completionInventory(command, app, options, omitSelectedSource)
	if !ok {
		return nil, noFileCompletion
	}
	allowed := make(map[research.Kind]bool, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = true
	}
	displays := completionDisplayCodes(inventory.Documents)
	builder := newCompletionBuilder(toComplete)
	if includeSpecial {
		if inventory.Project != nil {
			builder.add(record.ProjectFile, "canonical Project")
		}
		if inventory.Policy != nil {
			builder.add(record.PolicyFile, "canonical Policy")
		}
	}
	for _, document := range inventory.Documents {
		id, found := document.ID()
		if !found || len(allowed) > 0 && !allowed[id.Kind()] {
			continue
		}
		display, found := displays[id]
		if !found {
			continue
		}
		fullID := id.String()
		sourceKey := ""
		if sourceReferences {
			if source, isSource := document.Record.(*research.Source); isSource {
				sourceKey = source.Key
			}
		}
		if !completionPrefixMatches(toComplete, display, fullID, sourceKey) {
			continue
		}
		description := recordCompletionDescription(document, display)
		builder.add(display, description)
		builder.add(fullID, description)
		if sourceKey != "" {
			builder.add(sourceKey, description)
		}
	}
	return builder.values(), builder.directive(command)
}

func completionPrefixMatches(prefix string, values ...string) bool {
	if prefix == "" {
		return true
	}
	prefix = strings.ToLower(prefix)
	for _, value := range values {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			return true
		}
	}
	return false
}

func completionDisplayCodes(documents []*record.Document) map[research.ID]string {
	byKind := make(map[research.Kind][]research.ID)
	for _, document := range documents {
		if id, found := document.ID(); found && !id.IsZero() {
			byKind[id.Kind()] = append(byKind[id.Kind()], id)
		}
	}
	result := make(map[research.ID]string)
	for kind, ids := range byKind {
		sort.Slice(ids, func(left, right int) bool { return ids[left].UUIDHex() < ids[right].UUIDHex() })
		letter, err := kind.DisplayLetter()
		if err != nil {
			continue
		}
		for index, id := range ids {
			hexValue := strings.ToUpper(id.UUIDHex())
			length := 8
			if index > 0 {
				length = max(length, commonPrefixLength(hexValue, strings.ToUpper(ids[index-1].UUIDHex()))+1)
			}
			if index+1 < len(ids) {
				length = max(length, commonPrefixLength(hexValue, strings.ToUpper(ids[index+1].UUIDHex()))+1)
			}
			if length <= len(hexValue) {
				result[id] = fmt.Sprintf("%c-%s", letter, hexValue[:length])
			}
		}
	}
	return result
}

func commonPrefixLength(left, right string) int {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	return index
}

func completionInventory(command *cobra.Command, app *App, options *rootOptions, omitSelectedSource bool) (*record.Inventory, bool) {
	if command == nil || app == nil || options == nil || app.ResolveWorkspace == nil || app.NewTransactionalStore == nil {
		return nil, false
	}
	start, err := app.startDir(options.startDir)
	if err != nil {
		return nil, false
	}
	source := options.source
	if omitSelectedSource {
		source = ""
	}
	resolved, err := app.ResolveWorkspace.Resolve(command.Context(), workspace.ResolveRequest{
		InvocationDir: start,
		Workspace:     options.workspace,
		Source:        source,
		SkipConfig:    true,
	})
	if err != nil || resolved == nil || resolved.Project == nil {
		return nil, false
	}
	store, err := app.NewTransactionalStore(resolved.Project)
	if err != nil || store == nil {
		return nil, false
	}
	inventory, err := store.Inventory(command.Context())
	if err != nil || inventory == nil || !inventory.Valid() {
		return nil, false
	}
	return inventory, true
}

func recordCompletionDescription(document *record.Document, display string) string {
	parts := []string{document.Kind().String(), display}
	if state := recordState(document); state != "" {
		parts = append(parts, state)
	}
	if common := document.Record.GetCommon(); common != nil && strings.TrimSpace(common.Title) != "" {
		parts = append(parts, common.Title)
	}
	if id, found := document.ID(); found {
		parts = append(parts, id.String())
	}
	return strings.Join(parts, " | ")
}

func recordState(document *record.Document) string {
	if document == nil {
		return ""
	}
	switch value := document.Record.(type) {
	case *research.Source:
		return string(value.State)
	case *research.Idea:
		return string(value.State)
	case *research.Plan:
		return string(value.State)
	case *research.Try:
		return string(value.State)
	case *research.Experiment:
		return string(value.Lifecycle)
	case *research.Attempt:
		return string(value.State)
	case *research.Evaluation:
		return string(value.Outcome)
	case *research.Release:
		return string(value.State)
	case *research.Promotion:
		return string(value.Outcome)
	default:
		return ""
	}
}

func completeWorkspaces(app *App) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if app == nil || app.Associations == nil {
			return nil, noFileCompletion
		}
		associations, err := app.Associations.List(command.Context())
		if err != nil {
			return nil, noFileCompletion
		}
		builder := newCompletionBuilder(toComplete)
		for _, association := range associations.Projects {
			builder.add(association.ProjectID.String(), association.Root)
			builder.add(association.Root, "Project "+association.ProjectID.String())
		}
		return builder.values(), noFileCompletion
	}
}

func completeWorkspaceBackends(app *App) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if app == nil || app.WorkspaceRegistry == nil {
			return nil, noFileCompletion
		}
		builder := newCompletionBuilder(toComplete)
		for _, descriptor := range app.WorkspaceRegistry.Descriptors() {
			kind := "optional local provider"
			if descriptor.BuiltIn {
				kind = "built-in correctness baseline"
			}
			builder.add(descriptor.Name, kind)
		}
		return builder.values(), noFileCompletion
	}
}

func completeMLflowProfiles(app *App, options *rootOptions) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		resolved, ok := completionWorkspace(command, app, options, false)
		if !ok || resolved.Config == nil {
			return nil, noFileCompletion
		}
		names := make([]string, 0, len(resolved.Config.Effective.MLflow.Profiles))
		for name := range resolved.Config.Effective.MLflow.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		builder := newCompletionBuilder(toComplete)
		for _, name := range names {
			profile := resolved.Config.Effective.MLflow.Profiles[name]
			builder.add(name, fmt.Sprintf("context=%s | binary=%s", profile.Context, profile.Binary))
		}
		return builder.values(), noFileCompletion
	}
}

func completionWorkspace(command *cobra.Command, app *App, options *rootOptions, skipConfig bool) (*workspace.Context, bool) {
	if command == nil || app == nil || options == nil || app.ResolveWorkspace == nil {
		return nil, false
	}
	start, err := app.startDir(options.startDir)
	if err != nil {
		return nil, false
	}
	resolved, err := app.ResolveWorkspace.Resolve(command.Context(), workspace.ResolveRequest{
		InvocationDir: start,
		Workspace:     options.workspace,
		Source:        options.source,
		SkipConfig:    skipConfig,
	})
	return resolved, err == nil && resolved != nil && resolved.Project != nil
}

func completeAgentProfiles(app *App) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		configuration, ok := completionAgentConfig(command, app)
		if !ok {
			return nil, noFileCompletion
		}
		names := make([]string, 0, len(configuration.Profiles))
		for name := range configuration.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		builder := newCompletionBuilder(toComplete)
		for _, name := range names {
			profile := configuration.Profiles[name]
			description := profile.Executable
			if profile.ReportedModel != "" {
				description += " | model=" + profile.ReportedModel
			}
			builder.add(name, description)
		}
		return builder.values(), noFileCompletion
	}
}

func completeAgentRoles(app *App) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		configuration, ok := completionAgentConfig(command, app)
		if !ok {
			return nil, noFileCompletion
		}
		roles := make([]string, 0, len(configuration.Roles))
		for role := range configuration.Roles {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		builder := newCompletionBuilder(toComplete)
		for _, role := range roles {
			builder.add(role, "profile="+configuration.Roles[role])
		}
		return builder.values(), noFileCompletion
	}
}

func completionAgentConfig(command *cobra.Command, app *App) (agentcli.Config, bool) {
	if command == nil || app == nil || app.LoadAgentConfig == nil || app.ResolveAgentConfigPath == nil {
		return agentcli.Config{}, false
	}
	path, err := command.Flags().GetString("config")
	if err != nil {
		return agentcli.Config{}, false
	}
	if path == "" {
		path, err = app.ResolveAgentConfigPath()
		if err != nil {
			return agentcli.Config{}, false
		}
	}
	configuration, err := app.LoadAgentConfig(path)
	return configuration, err == nil
}

func completeTargets(app *App, options *rootOptions) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		inventory, ok := completionInventory(command, app, options, false)
		if !ok {
			return nil, noFileCompletion
		}
		targets := map[string]string{}
		for _, document := range inventory.Documents {
			switch value := document.Record.(type) {
			case *research.Release:
				targets[value.Target] = "Release target"
			case *research.PromotionSpec:
				targets[value.Target] = "Promotion target"
			case *research.Promotion:
				targets[value.Target] = "Promotion target"
			}
		}
		names := make([]string, 0, len(targets))
		for target := range targets {
			names = append(names, target)
		}
		sort.Strings(names)
		builder := newCompletionBuilder(toComplete)
		for _, target := range names {
			builder.add(target, targets[target])
		}
		return builder.values(), noFileCompletion
	}
}

func completeResourceNeeds(app *App, options *rootOptions) cobra.CompletionFunc {
	return func(command *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if strings.Contains(toComplete, ":") {
			return nil, noFileCompletion
		}
		inventory, ok := completionInventory(command, app, options, false)
		if !ok {
			return nil, noFileCompletion
		}
		candidates := make([]research.ReferenceCandidate, 0)
		for _, document := range inventory.OfKind(research.KindResourcePool) {
			id, found := document.ID()
			if found {
				candidates = append(candidates, research.ReferenceCandidate{ID: id})
			}
		}
		builder := newCompletionBuilder(toComplete)
		for _, document := range inventory.OfKind(research.KindResourcePool) {
			id, found := document.ID()
			if !found {
				continue
			}
			display, err := research.DisplayCode(id, candidates)
			if err != nil {
				continue
			}
			description := recordCompletionDescription(document, display) + " | enter UNITS:HOURS"
			builder.add(display+":", description)
			builder.add(id.String()+":", description)
		}
		return builder.values(), noFileCompletion
	}
}

func recordKindNames() []string {
	values := make([]string, 0, len(research.RecordKinds))
	for _, kind := range research.RecordKinds {
		values = append(values, kind.String())
	}
	return values
}

const maxCompletionItems = 100

type completionBuilder struct {
	prefix    string
	seen      map[string]struct{}
	items     []string
	truncated bool
}

func newCompletionBuilder(prefix string) *completionBuilder {
	return &completionBuilder{prefix: prefix, seen: map[string]struct{}{}}
}

func (builder *completionBuilder) add(value, description string) {
	value, ok := sanitizeCompletionValue(value)
	if !ok || builder.prefix != "" && !strings.HasPrefix(strings.ToLower(value), strings.ToLower(builder.prefix)) {
		return
	}
	if _, duplicate := builder.seen[value]; duplicate {
		return
	}
	if len(builder.items) >= maxCompletionItems {
		builder.truncated = true
		return
	}
	builder.seen[value] = struct{}{}
	if description = sanitizeCompletionDescription(description); description != "" {
		value = cobra.CompletionWithDesc(value, description)
	}
	builder.items = append(builder.items, value)
}

func (builder *completionBuilder) values() []string {
	sort.SliceStable(builder.items, func(left, right int) bool {
		leftValue := strings.SplitN(builder.items[left], "\t", 2)[0]
		rightValue := strings.SplitN(builder.items[right], "\t", 2)[0]
		return leftValue < rightValue
	})
	return builder.items
}

func (builder *completionBuilder) directive(command *cobra.Command) cobra.ShellCompDirective {
	if builder != nil && builder.truncated {
		if command != nil {
			command.PrintErrln("exp: completion results were capped; type a longer prefix")
		}
		return noFileCompletion | cobra.ShellCompDirectiveError
	}
	return noFileCompletion
}

func sanitizeCompletionValue(value string) (string, bool) {
	if value == "" || !utf8.ValidString(value) || safeDiagnosticText(value) != value {
		return "", false
	}
	for _, character := range value {
		if character == '\t' || character == '\r' || character == '\n' || character == 0 || unicode.IsControl(character) {
			return "", false
		}
	}
	return value, true
}

func sanitizeCompletionDescription(description string) string {
	if description == "" {
		return ""
	}
	description = safeDiagnosticText(description)
	description = strings.Map(func(character rune) rune {
		if character == '\t' || character == '\r' || character == '\n' || unicode.IsControl(character) {
			return ' '
		}
		return character
	}, description)
	return strings.Join(strings.Fields(description), " ")
}
