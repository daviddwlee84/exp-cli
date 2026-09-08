package cli

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const commandReferenceExcludedAnnotation = "exp.cli/command-reference-excluded"

//go:embed guides/*.md
var embeddedGuides embed.FS

type guideSpec struct {
	filename string
	summary  string
}

var guideCatalog = map[string]guideSpec{
	"exploration": {filename: "guides/exploration.md", summary: "start adhoc analyses, remember storage preferences, and retrieve results"},
	"setup":       {filename: "guides/setup.md", summary: "initialize canonical authority and local registration"},
	"quick":       {filename: "guides/quick.md", summary: "run and conclude a bounded exploratory Try"},
	"research":    {filename: "guides/research.md", summary: "move from Ideas through comparable evidence"},
	"promotion":   {filename: "guides/promotion.md", summary: "package evidence and preserve the human production gate"},
	"config":      {filename: "guides/config.md", summary: "understand layered preferences and exact-digest trust"},
	"workspaces":  {filename: "guides/workspaces.md", summary: "resolve Projects, Sources, clones, and workspace backends"},
}

func newGuideCommand(app *App) *cobra.Command {
	topics := guideTopicNames()
	valid := make([]string, 0, len(topics))
	for _, topic := range topics {
		valid = append(valid, cobra.CompletionWithDesc(topic, guideCatalog[topic].summary))
	}
	command := &cobra.Command{
		Use:   "guide [setup|quick|exploration|research|promotion|config|workspaces]",
		Short: "Read conceptual workflow guides",
		Long: `Read concise conceptual guides that explain when commands fit together.

This is separate from "exp help COMMAND", which remains Cobra's standard
syntax and flag help path. Run without a topic to see the guide index.`,
		Args:        cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs:   valid,
		Annotations: map[string]string{commandReferenceExcludedAnnotation: "true"},
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeGuideIndex(app, topics)
			}
			return writeGuideTopic(app, args[0])
		},
	}
	return command
}

func guideTopicNames() []string {
	topics := make([]string, 0, len(guideCatalog))
	for topic := range guideCatalog {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}

func writeGuideIndex(app *App, topics []string) error {
	var body strings.Builder
	body.WriteString("# exp guides\n\n")
	body.WriteString(flowMaps["root"])
	body.WriteString("\n\n")
	table := newHumanTable("TOPIC", "ABOUT")
	for _, topic := range topics {
		table.Add(topic, guideCatalog[topic].summary)
	}
	body.WriteString(mustRenderTable(table))
	body.WriteString("\nRead one with: exp guide <topic>\n")
	safe := safeHumanOutput(body.String())
	return successfulOutputError(app.WriteHuman(renderGuideMarkdown(safe, app.outStyle())))
}

func writeGuideTopic(app *App, topic string) error {
	spec, found := guideCatalog[topic]
	if !found {
		return safeCLIError(fmt.Errorf("unknown guide topic %q; run \"exp guide\" for available topics", topic))
	}
	content, err := embeddedGuides.ReadFile(spec.filename)
	if err != nil {
		return safeCLIError(fmt.Errorf("read embedded guide %s: %w", topic, err))
	}
	safe := safeHumanOutput(string(content))
	return successfulOutputError(app.WriteHuman(renderGuideMarkdown(safe, app.outStyle())))
}
