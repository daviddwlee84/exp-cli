//go:build aix

package exploration

import (
	"context"
	"errors"
)

func Search(context.Context, map[string][]Card, HistoryQuery) ([]Card, error) {
	return nil, errors.New("SQLite history search is unavailable on AIX; use record list/show")
}
