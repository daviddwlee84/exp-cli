//go:build !aix

package exploration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/exp-cli/internal/localstate"
	"github.com/daviddwlee84/exp-cli/internal/pathx"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// Search refreshes only the selected Projects in a disposable SQLite index.
// Canonical records are always read by the caller before consulting the cache.
func Search(ctx context.Context, projects map[string][]Card, query HistoryQuery) ([]Card, error) {
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, errors.New("history limit must be between 1 and 1000")
	}
	home, err := localstate.CacheHome()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "exp", "history")
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	result := []Card{}
	err = localstate.WithLockedFile(ctx, filepath.Join(dir, "index.lock"), func(root *os.Root, _ string) error {
		name := "index.sqlite"
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			if err := file.Close(); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		if _, err := pathx.CheckPrivateFile(root, name, 0o600, "history index"); err != nil {
			return err
		}
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dir, name))}
		db, err := sql.Open("sqlite3", u.String()+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return err
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cards(project TEXT NOT NULL,id TEXT NOT NULL,kind TEXT,state TEXT,updated TEXT,sources TEXT,versions TEXT,search TEXT,payload TEXT,PRIMARY KEY(project,id))`); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for project, cards := range projects {
			if _, err := tx.ExecContext(ctx, "DELETE FROM cards WHERE project=?", project); err != nil {
				return err
			}
			for _, card := range cards {
				payload, err := json.Marshal(card)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO cards VALUES(?,?,?,?,?,?,?,?,?)", project, card.ID, card.Kind, card.State, card.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), "|"+strings.Join(card.Sources, "|")+"|", strings.Join(card.Versions, " "), card.SearchText, string(payload)); err != nil {
					return err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if len(projects) == 0 {
			return nil
		}
		clauses := []string{}
		args := []any{}
		placeholders := []string{}
		for project := range projects {
			placeholders = append(placeholders, "?")
			args = append(args, project)
		}
		clauses = append(clauses, "project IN ("+strings.Join(placeholders, ",")+")")
		for _, term := range strings.Fields(strings.ToLower(query.Text)) {
			clauses = append(clauses, "instr(search,?) > 0")
			args = append(args, term)
		}
		for _, filter := range []struct{ column, value string }{{"kind", query.Kind}, {"state", query.State}} {
			if filter.value != "" {
				clauses = append(clauses, filter.column+"=?")
				args = append(args, filter.value)
			}
		}
		if query.Source != "" {
			clauses = append(clauses, "instr(sources,?) > 0")
			args = append(args, "|"+query.Source+"|")
		}
		if query.Version != "" {
			clauses = append(clauses, "instr(versions,?) > 0")
			args = append(args, query.Version)
		}
		if !query.After.IsZero() {
			clauses = append(clauses, "updated>=?")
			args = append(args, query.After.UTC().Format("2006-01-02T15:04:05.000000000Z"))
		}
		if !query.Before.IsZero() {
			clauses = append(clauses, "updated<?")
			args = append(args, query.Before.UTC().Format("2006-01-02T15:04:05.000000000Z"))
		}
		args = append(args, query.Limit)
		rows, err := db.QueryContext(ctx, "SELECT payload FROM cards WHERE "+strings.Join(clauses, " AND ")+" ORDER BY updated DESC,project,id LIMIT ?", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var payload string
			if err := rows.Scan(&payload); err != nil {
				return err
			}
			var card Card
			if err := json.Unmarshal([]byte(payload), &card); err != nil {
				return err
			}
			result = append(result, card)
		}
		return rows.Err()
	})
	return result, err
}
