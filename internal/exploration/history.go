package exploration

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

type Card struct {
	Project     string     `json:"project"`
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Summary     string     `json:"summary,omitempty"`
	State       string     `json:"state,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Sources     []string   `json:"sources"`
	Versions    []string   `json:"versions"`
	Tags        []string   `json:"tags"`
	Artifacts   []Artifact `json:"artifacts"`
	Try         string     `json:"try,omitempty"`
	SearchText  string     `json:"-"`
}

type HistoryQuery struct {
	Text    string
	Kind    string
	Source  string
	Version string
	State   string
	After   time.Time
	Before  time.Time
	Limit   int
}

func Cards(project string, inventory *record.Inventory) []Card {
	result := []Card{}
	bodies := map[string]string{}
	sources := map[research.ID]string{}
	tries := map[research.ID]*research.Try{}
	for _, document := range inventory.OfKind(research.KindSource) {
		s := document.Record.(*research.Source)
		sources[s.ID] = s.Key
	}
	for _, document := range inventory.OfKind(research.KindTry) {
		v := document.Record.(*research.Try)
		tries[v.ID] = v
	}
	for _, document := range inventory.Documents {
		common := document.Record.GetCommon()
		if common == nil {
			continue
		}
		card := Card{Project: project, ID: common.ID.String(), Kind: string(document.Kind()), Title: common.Title, UpdatedAt: common.UpdatedAt,
			Tags: append([]string{}, common.Tags...), Sources: []string{}, Versions: []string{}, Artifacts: []Artifact{}}
		addSource := func(id research.ID) {
			card.Sources = append(card.Sources, id.String())
			if key := sources[id]; key != "" {
				card.Sources = append(card.Sources, key)
			}
		}
		switch v := document.Record.(type) {
		case *research.Try:
			card.Description = v.Goal
			card.State = string(v.State)
			for _, id := range v.Sources {
				addSource(id)
			}
			if v.Conclusion != nil {
				card.Summary = v.Conclusion.Summary
			}
			if table := v.Extensions[Namespace]; table != nil {
				if text, ok := table["summary"].(string); ok {
					card.Summary = text
				}
				if text, ok := table["summary_at"].(string); ok {
					if at, err := time.Parse(time.RFC3339Nano, text); err == nil && at.After(card.UpdatedAt) {
						card.UpdatedAt = at
					}
				}
			}
		case *research.Attempt:
			card.State = string(v.State)
			if !v.Try.IsZero() {
				card.Try = v.Try.String()
				if owner := tries[v.Try]; owner != nil {
					card.Description = owner.Goal
					card.Tags = append(card.Tags, owner.Tags...)
				}
			}
			for _, snapshot := range v.SourceSnapshots {
				addSource(snapshot.Source)
				card.Versions = append(card.Versions, snapshot.HeadCommit)
			}
			if v.Provenance != nil && v.Provenance.GitCommit != "" {
				card.Versions = append(card.Versions, v.Provenance.GitCommit)
			}
			if metadata, err := MetadataFor(v); err == nil && metadata != nil {
				card.Artifacts = metadata.Artifacts
				if metadata.Runner != nil {
					card.Versions = append(card.Versions, metadata.Runner.Version, metadata.Runner.BuildCommit, metadata.Runner.ExecutableDigest)
				}
			}
		case *research.Experiment:
			card.State = string(v.Lifecycle)
			card.Description = v.Design.Question
		case *research.Source:
			card.State = string(v.State)
			addSource(v.ID)
		}
		if card.Description == "" {
			card.Description = strings.TrimSpace(document.Body)
			if len([]rune(card.Description)) > 240 {
				card.Description = string([]rune(card.Description)[:240]) + "…"
			}
		}
		encoded, _ := json.Marshal(card)
		card.SearchText = strings.ToLower(string(encoded) + "\n" + document.Body)
		result = append(result, card)
		bodies[card.ID] = document.Body
	}
	owners := map[string]int{}
	for i, card := range result {
		if card.Kind == "try" {
			owners[card.ID] = i
		}
	}
	for i := range result {
		card := &result[i]
		owner, ok := owners[card.Try]
		if !ok || card.Try == "" {
			continue
		}
		result[owner].Versions = append(result[owner].Versions, card.Versions...)
		result[owner].Artifacts = append(result[owner].Artifacts, card.Artifacts...)
		card.Summary = result[owner].Summary
	}
	for i := range result {
		encoded, _ := json.Marshal(result[i])
		result[i].SearchText = strings.ToLower(string(encoded) + "\n" + bodies[result[i].ID])
	}
	return result
}
