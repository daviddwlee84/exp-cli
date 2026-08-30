package harnessv0

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
)

var (
	roadmapActivePattern = regexp.MustCompile(`^- \[ \] \*\*\[(?:\?/)?(S|M|L|XL)\] ([^*]+)\*\*\s*(?:—|-)?\s*(.*)$`)
	roadmapDonePattern   = regexp.MustCompile(`^- ✅ \[([0-9]{4}-[0-9]{2}-[0-9]{2})\] \[(P[123?])/(S|M|L|XL)\] (.+?)\s+→\s+(#[0-9]{3,})(?:\s+\(([^)]*)\))?`)
	ledgerPattern        = regexp.MustCompile(`^-\s+(?:~~)?\*\*(F-[0-9]{3,})\*\*(?:~~)?\s+\[([0-9]{4}-[0-9]{2}-[0-9]{2})\]\s+\((#[0-9]{3,}|ext)\)\s+(.+)$`)
	experimentRefPattern = regexp.MustCompile(`#[0-9]{3,}`)
	findingRefPattern    = regexp.MustCompile(`F-[0-9]{3,}`)
)

type parsedTree struct {
	Reports     []legacyReport
	Plans       []legacyPlan
	Findings    []legacyFinding
	Inbox       []legacyInbox
	Diagnostics []Diagnostic
}

type legacyReport struct {
	File      *sourceData
	Start     int64
	End       int64
	Alias     string
	Fields    map[string]string
	Lists     map[string][]string
	Body      string
	BodyStart int64
}

type legacyPlan struct {
	File       *sourceData
	Start      int64
	End        int64
	Lane       string
	Effort     string
	Title      string
	Text       string
	Payoff     string
	Category   string
	DependsOn  []string
	Done       bool
	DoneDate   string
	Experiment string
}

type legacyFinding struct {
	File         *sourceData
	Start        int64
	End          int64
	Alias        string
	Date         string
	Experiment   string
	Statement    string
	Raw          string
	Overturned   bool
	WeakenedBy   string
	OverturnedBy string
}

type legacyInbox struct {
	File  *sourceData
	Start int64
	End   int64
	Text  string
}

type markedSpan struct {
	start, end int64
	kind       string
	mapping    string
}

type lineSpan struct {
	start, end int
	text       string
}

func parseTree(tree *sourceTree) (*parsedTree, error) {
	parsed := &parsedTree{}
	for index := range tree.Files {
		file := &tree.Files[index]
		if !utf8.Valid(file.Data) {
			key := "file:" + file.Path + ":invalid-utf8"
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "source.invalid_utf8", Message: "invalid UTF-8 blocks semantic migration; exact bytes remain archivable", Path: file.Path})
			file.Spans = []Span{{Kind: "unknown", StartByte: 0, EndByte: int64(len(file.Data)), SHA256: hashBytes(file.Data)}}
			continue
		}
		switch {
		case file.Path == "ROADMAP.md":
			parseRoadmap(file, parsed)
		case file.Path == "LEDGER.md":
			parseLedger(file, parsed)
		case file.Path == "INBOX.md":
			parseInbox(file, parsed)
		case strings.HasSuffix(file.Path, "/REPORT.md"):
			parseReport(file, parsed)
		default:
			file.Spans = []Span{{Kind: "body", StartByte: 0, EndByte: int64(len(file.Data)), SHA256: hashBytes(file.Data)}}
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{State: "info", Code: "source.curated_view", Message: "curated/generated view is archived as a non-authoritative snapshot", Path: file.Path})
		}
		if err := validateSpanCoverage(file); err != nil {
			return nil, err
		}
	}
	analyzeAmbiguities(tree, parsed)
	sort.Slice(parsed.Diagnostics, func(i, j int) bool {
		if parsed.Diagnostics[i].Path != parsed.Diagnostics[j].Path {
			return parsed.Diagnostics[i].Path < parsed.Diagnostics[j].Path
		}
		if parsed.Diagnostics[i].StartByte != parsed.Diagnostics[j].StartByte {
			return parsed.Diagnostics[i].StartByte < parsed.Diagnostics[j].StartByte
		}
		return parsed.Diagnostics[i].Code < parsed.Diagnostics[j].Code
	})
	return parsed, nil
}

func analyzeAmbiguities(tree *sourceTree, parsed *parsedTree) {
	reports := map[string]legacyReport{}
	for _, report := range parsed.Reports {
		reports[report.Alias] = report
		if report.Fields["mlflow"] == "" {
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{State: "info", Code: "report.mlflow_absent", Message: "empty MLflow value is treated as valid absence", Path: report.File.Path})
		}
		status := report.Fields["status"]
		if (strings.HasPrefix(status, "concluded-") || status == "inconclusive" || status == "superseded") && !strings.Contains(report.Body, "## Provenance") {
			key := "report:" + report.Alias + ":provenance"
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "report.provenance", Message: "concluded report has dirty or incomplete provenance", Path: report.File.Path})
		}
	}
	ledgerByExperiment := map[string]map[string]bool{}
	for _, finding := range parsed.Findings {
		if strings.HasPrefix(finding.Experiment, "#") {
			if ledgerByExperiment[finding.Experiment] == nil {
				ledgerByExperiment[finding.Experiment] = map[string]bool{}
			}
			ledgerByExperiment[finding.Experiment][finding.Alias] = true
		}
		for _, target := range markdownLinkTargets(finding.Statement) {
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			candidate := filepath.Join(tree.Root, filepath.FromSlash(target))
			inside, err := pathx.Contains(tree.Root, candidate)
			info, statErr := os.Lstat(candidate)
			if err != nil || !inside || statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				key := "finding:" + finding.Alias + ":evidence-link"
				message := "finding evidence link is broken or unsafe and remains raw in the archive"
				if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
					message = "finding evidence link could not be inspected and remains raw in the archive"
				}
				parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "finding.evidence_link", Message: message, Path: finding.File.Path, StartByte: finding.Start, EndByte: finding.End})
			}
		}
	}
	for alias, report := range reports {
		front := stringSet(report.Lists["findings"])
		ledger := ledgerByExperiment[alias]
		for finding := range unionKeys(front, ledger) {
			if front[finding] == ledger[finding] {
				continue
			}
			key := "relationship:" + alias + ":" + finding
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "relationship.report_ledger_drift", Message: "report/ledger inverse Finding references disagree; the standalone Finding edge is authoritative only after review", Path: report.File.Path})
		}
		if conclusion := section(report.Body, "Conclusion"); conclusion != "" {
			bodyFindings := stringSet(findingRefPattern.FindAllString(conclusion, -1))
			for finding := range unionKeys(front, bodyFindings) {
				if front[finding] == bodyFindings[finding] {
					continue
				}
				key := "report:" + alias + ":body-finding:" + finding
				parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "report.finding_mismatch", Message: "REPORT front matter and Conclusion body disagree about Findings", Path: report.File.Path})
			}
		}
	}
	for _, plan := range parsed.Plans {
		if plan.Done {
			continue
		}
		planTitle := normalizedTitle(plan.Title)
		for alias, report := range reports {
			if planTitle != "" && planTitle == normalizedTitle(report.Fields["title"]) {
				key := fmt.Sprintf("plan:ROADMAP.md:%d:%d:%s", plan.Start, plan.End, strings.TrimPrefix(hashBytes(plan.File.Data[plan.Start:plan.End]), "sha256:"))
				parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "roadmap.queued_completed_drift", Message: "roadmap item is still queued although an Experiment with the same normalized title exists (" + alias + ")", Path: plan.File.Path, StartByte: plan.Start, EndByte: plan.End})
			}
		}
	}
}

func markdownLinkTargets(value string) []string {
	pattern := regexp.MustCompile(`\[[^]]*\]\(([^)]+)\)`)
	matches := pattern.FindAllStringSubmatch(value, -1)
	output := make([]string, 0, len(matches))
	for _, match := range matches {
		output = append(output, strings.TrimSpace(match[1]))
	}
	return output
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	return set
}

func unionKeys(left, right map[string]bool) map[string]bool {
	output := map[string]bool{}
	for key := range left {
		output[key] = true
	}
	for key := range right {
		output[key] = true
	}
	return output
}

func section(body, name string) string {
	marker := "## " + name
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	remaining := body[start+len(marker):]
	if end := strings.Index(remaining, "\n## "); end >= 0 {
		remaining = remaining[:end]
	}
	return remaining
}

func normalizedTitle(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(regexp.MustCompile(`[^[:alnum:]]+`).ReplaceAllString(value, " ")), " "))
}

func parseRoadmap(file *sourceData, parsed *parsedTree) {
	lines := splitLines(file.Data)
	lane := ""
	var marks []markedSpan
	for index := 0; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index].text)
		if strings.HasPrefix(trimmed, "## ") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			switch name {
			case "P1", "P2", "P3", "P?", "Done":
				lane = name
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "- [ ]") && !strings.HasPrefix(trimmed, "- ✅") {
			continue
		}
		endIndex := index + 1
		for endIndex < len(lines) {
			next := strings.TrimSpace(lines[endIndex].text)
			if strings.HasPrefix(next, "## ") || strings.HasPrefix(next, "- [ ]") || strings.HasPrefix(next, "- ✅") {
				break
			}
			endIndex++
		}
		start, end := int64(lines[index].start), int64(lines[endIndex-1].end)
		raw := string(file.Data[start:end])
		oneLine := strings.Join(strings.Fields(raw), " ")
		if match := roadmapActivePattern.FindStringSubmatch(oneLine); match != nil && lane != "Done" {
			stable := fmt.Sprintf("ROADMAP.md:%d:%d:%s", start, end, strings.TrimPrefix(hashBytes(file.Data[start:end]), "sha256:"))
			key := "plan:" + stable
			payoff, category, dependencies := parseRoadmapTail(match[3])
			parsed.Plans = append(parsed.Plans, legacyPlan{File: file, Start: start, End: end, Lane: lane, Effort: match[1], Title: strings.TrimSpace(match[2]), Text: raw, Payoff: payoff, Category: category, DependsOn: dependencies})
			marks = append(marks, markedSpan{start: start, end: end, kind: "parsed", mapping: key})
		} else if match := roadmapDonePattern.FindStringSubmatch(oneLine); match != nil && lane == "Done" {
			stable := fmt.Sprintf("ROADMAP.md:%d:%d:%s", start, end, strings.TrimPrefix(hashBytes(file.Data[start:end]), "sha256:"))
			key := "plan:" + stable
			parsed.Plans = append(parsed.Plans, legacyPlan{File: file, Start: start, End: end, Lane: match[2], Effort: match[3], Title: strings.TrimSpace(match[4]), Text: raw, Done: true, DoneDate: match[1], Experiment: match[5]})
			marks = append(marks, markedSpan{start: start, end: end, kind: "parsed", mapping: key})
		} else {
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{State: "warning", Code: "roadmap.unparsed_item", Message: "roadmap item syntax is not recognized and remains only in the archive", Path: file.Path, StartByte: start, EndByte: end})
		}
		index = endIndex - 1
	}
	file.Spans = completeSpans(file.Data, marks)
}

func parseRoadmapTail(value string) (string, string, []string) {
	open := strings.LastIndex(value, "(")
	close := strings.LastIndex(value, ")")
	if open < 0 || close <= open {
		return "", "", nil
	}
	fields := strings.Split(value[open+1:close], ";")
	var payoff, category string
	var dependencies []string
	for _, field := range fields {
		key, raw, found := strings.Cut(strings.TrimSpace(field), ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "payoff":
			payoff = strings.TrimSpace(raw)
		case "cat":
			category = strings.TrimSpace(raw)
		case "depends-on":
			dependencies = append(dependencies, findingRefPattern.FindAllString(raw, -1)...)
			dependencies = append(dependencies, experimentRefPattern.FindAllString(raw, -1)...)
		}
	}
	return payoff, category, dependencies
}

func parseLedger(file *sourceData, parsed *parsedTree) {
	lines := splitLines(file.Data)
	var marks []markedSpan
	overturnedLane := false
	for index := 0; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index].text)
		if trimmed == "## Active" {
			overturnedLane = false
			continue
		}
		if trimmed == "## Overturned" {
			overturnedLane = true
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		endIndex := index + 1
		for endIndex < len(lines) {
			next := strings.TrimSpace(lines[endIndex].text)
			if strings.HasPrefix(next, "## ") || strings.HasPrefix(next, "- ") {
				break
			}
			endIndex++
		}
		start, end := int64(lines[index].start), int64(lines[endIndex-1].end)
		raw := string(file.Data[start:end])
		oneLine := strings.Join(strings.Fields(raw), " ")
		match := ledgerPattern.FindStringSubmatch(oneLine)
		if match == nil {
			parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{State: "warning", Code: "ledger.unparsed_item", Message: "ledger item syntax is not recognized and remains only in the archive", Path: file.Path, StartByte: start, EndByte: end})
			index = endIndex - 1
			continue
		}
		statement := strings.TrimSpace(match[4])
		weakened := ""
		if marker := regexp.MustCompile(`\(weakened by (F-[0-9]{3,})\)`).FindStringSubmatch(statement); marker != nil {
			weakened = marker[1]
		}
		overturnedBy := ""
		if marker := regexp.MustCompile(`overturned [0-9]{4}-[0-9]{2}-[0-9]{2} by (F-[0-9]{3,})`).FindStringSubmatch(statement); marker != nil {
			overturnedBy = marker[1]
		}
		key := "finding:" + match[1]
		parsed.Findings = append(parsed.Findings, legacyFinding{File: file, Start: start, End: end, Alias: match[1], Date: match[2], Experiment: match[3], Statement: statement, Raw: raw, Overturned: overturnedLane, WeakenedBy: weakened, OverturnedBy: overturnedBy})
		marks = append(marks, markedSpan{start: start, end: end, kind: "parsed", mapping: key})
		index = endIndex - 1
	}
	file.Spans = completeSpans(file.Data, marks)
}

func parseInbox(file *sourceData, parsed *parsedTree) {
	lines := splitLines(file.Data)
	var marks []markedSpan
	for index := 0; index < len(lines); index++ {
		if !strings.HasPrefix(lines[index].text, "- ") {
			continue
		}
		endIndex := index + 1
		for endIndex < len(lines) && (strings.HasPrefix(lines[endIndex].text, "  ") || strings.TrimSpace(lines[endIndex].text) == "") {
			endIndex++
		}
		start, end := int64(lines[index].start), int64(lines[endIndex-1].end)
		text := string(file.Data[start:end])
		key := fmt.Sprintf("inbox:%s:%d:%d:%s", file.Path, start, end, strings.TrimPrefix(hashBytes(file.Data[start:end]), "sha256:"))
		parsed.Inbox = append(parsed.Inbox, legacyInbox{File: file, Start: start, End: end, Text: text})
		parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "inbox.no_canonical_destination", Message: "v1 has no lossless Idea destination; retain this item in the archive or resolve it explicitly", Path: file.Path, StartByte: start, EndByte: end})
		marks = append(marks, markedSpan{start: start, end: end, kind: "parsed", mapping: key})
		index = endIndex - 1
	}
	file.Spans = completeSpans(file.Data, marks)
}

func parseReport(file *sourceData, parsed *parsedTree) {
	lines := splitLines(file.Data)
	if len(lines) < 3 || strings.TrimSpace(lines[0].text) != "---" {
		file.Spans = completeSpans(file.Data, nil)
		parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: "report:" + file.Path + ":frontmatter", State: "needs_review", Code: "report.frontmatter_missing", Message: "REPORT.md has no recognized YAML-like front matter", Path: file.Path})
		return
	}
	closing := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index].text) == "---" {
			closing = index
			break
		}
	}
	if closing < 0 {
		file.Spans = completeSpans(file.Data, nil)
		parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: "report:" + file.Path + ":frontmatter", State: "needs_review", Code: "report.frontmatter_unclosed", Message: "REPORT.md front matter has no closing delimiter", Path: file.Path})
		return
	}
	known := map[string]bool{"id": true, "slug": true, "title": true, "status": true, "question": true, "axis": true, "baseline": true, "spec": true, "started": true, "concluded": true, "conclusion": true, "findings": true, "tags": true, "refs": true, "mlflow": true}
	fields := map[string]string{}
	lists := map[string][]string{}
	marks := []markedSpan{{start: int64(lines[0].start), end: int64(lines[0].end), kind: "formatting"}, {start: int64(lines[closing].start), end: int64(lines[closing].end), kind: "formatting"}}
	for index := 1; index < closing; index++ {
		line := strings.TrimSpace(lines[index].text)
		key, value, found := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !found || !known[key] {
			continue
		}
		value = strings.TrimSpace(value)
		if value == ">" {
			var folded []string
			for index+1 < closing && (strings.HasPrefix(lines[index+1].text, " ") || strings.HasPrefix(lines[index+1].text, "\t")) {
				index++
				folded = append(folded, strings.TrimSpace(lines[index].text))
			}
			value = strings.Join(folded, " ")
		}
		value = strings.Trim(value, `"'`)
		fields[key] = value
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			for _, item := range strings.Split(strings.TrimSpace(value[1:len(value)-1]), ",") {
				item = strings.Trim(strings.TrimSpace(item), `"'`)
				if item != "" {
					lists[key] = append(lists[key], item)
				}
			}
		}
		marks = append(marks, markedSpan{start: int64(lines[index].start), end: int64(lines[index].end), kind: "parsed"})
	}
	bodyStart := int64(lines[closing].end)
	if bodyStart < int64(len(file.Data)) {
		marks = append(marks, markedSpan{start: bodyStart, end: int64(len(file.Data)), kind: "body"})
	}
	alias := ""
	if id, err := strconv.Atoi(fields["id"]); err == nil && id >= 0 {
		alias = fmt.Sprintf("#%03d", id)
		if len(fields["id"]) > 3 {
			alias = "#" + fields["id"]
		}
	}
	mappingKey := "experiment:" + alias
	if alias != "" {
		for index := range marks {
			if marks[index].kind == "parsed" || marks[index].kind == "body" {
				marks[index].mapping = mappingKey
			}
		}
	}
	file.Spans = completeSpans(file.Data, marks)
	if alias == "" {
		key := "report:" + file.Path + ":id"
		parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Key: key, State: "needs_review", Code: "report.id", Message: "REPORT.md id is missing or invalid", Path: file.Path})
		return
	}
	parsed.Reports = append(parsed.Reports, legacyReport{File: file, Start: 0, End: int64(len(file.Data)), Alias: alias, Fields: fields, Lists: lists, Body: string(file.Data[bodyStart:]), BodyStart: bodyStart})
}

func completeSpans(data []byte, marks []markedSpan) []Span {
	sort.Slice(marks, func(i, j int) bool {
		if marks[i].start != marks[j].start {
			return marks[i].start < marks[j].start
		}
		return marks[i].end < marks[j].end
	})
	spans := make([]Span, 0, len(marks)*2+1)
	cursor := int64(0)
	for _, mark := range marks {
		if mark.start < cursor || mark.end < mark.start || mark.end > int64(len(data)) {
			continue
		}
		if mark.start > cursor {
			spans = append(spans, makeSpan(data, "unknown", cursor, mark.start, ""))
		}
		if mark.end > mark.start {
			spans = append(spans, makeSpan(data, mark.kind, mark.start, mark.end, mark.mapping))
		}
		cursor = mark.end
	}
	if cursor < int64(len(data)) {
		spans = append(spans, makeSpan(data, "unknown", cursor, int64(len(data)), ""))
	}
	if len(data) == 0 {
		return []Span{}
	}
	return spans
}

func makeSpan(data []byte, kind string, start, end int64, mapping string) Span {
	return Span{Kind: kind, StartByte: start, EndByte: end, SHA256: hashBytes(data[start:end]), MappingKey: mapping}
}

func validateSpanCoverage(file *sourceData) error {
	cursor := int64(0)
	for _, span := range file.Spans {
		if span.StartByte != cursor || span.EndByte < span.StartByte || span.EndByte > int64(len(file.Data)) || hashBytes(file.Data[span.StartByte:span.EndByte]) != span.SHA256 {
			return fmt.Errorf("source span coverage failed for %s at byte %d", file.Path, cursor)
		}
		cursor = span.EndByte
	}
	if cursor != int64(len(file.Data)) {
		return fmt.Errorf("source span coverage failed for %s: stopped at %d of %d", file.Path, cursor, len(file.Data))
	}
	return nil
}

func splitLines(data []byte) []lineSpan {
	if len(data) == 0 {
		return nil
	}
	lines := make([]lineSpan, 0, 32)
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		if end < len(data) {
			end++
		}
		textEnd := end
		for textEnd > start && (data[textEnd-1] == '\n' || data[textEnd-1] == '\r') {
			textEnd--
		}
		lines = append(lines, lineSpan{start: start, end: end, text: string(data[start:textEnd])})
		start = end
	}
	return lines
}
