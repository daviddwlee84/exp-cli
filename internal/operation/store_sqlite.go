//go:build !aix

package operation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const operationalRelative = "exp/runtime/v1/control.sqlite"

type Store struct {
	db    *sql.DB
	path  string
	clock func() time.Time
}

func PathFor(gitCommonDir string) (string, error) {
	if gitCommonDir == "" || !filepath.IsAbs(gitCommonDir) {
		return "", errors.New("Git common directory must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(gitCommonDir)
	if err != nil {
		return "", fmt.Errorf("canonicalize Git common directory: %w", err)
	}
	return filepath.Join(canonical, filepath.FromSlash(operationalRelative)), nil
}

func Open(ctx context.Context, gitCommonDir string, opts ...Option) (*Store, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if gitCommonDir == "" || !filepath.IsAbs(gitCommonDir) {
		return nil, errors.New("Git common directory must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(gitCommonDir)
	if err != nil {
		return nil, fmt.Errorf("canonicalize Git common directory: %w", err)
	}
	config := options{clock: time.Now}
	for _, option := range opts {
		if option != nil {
			option(&config)
		}
	}
	if config.clock == nil {
		config.clock = time.Now
	}

	databasePath, databaseIdentity, err := prepareOperationalDatabase(canonical)
	if err != nil {
		return nil, err
	}
	dsn := sqliteDSN(databasePath)
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open operational database: %w", err)
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(2)
	store := &Store{db: database, path: databasePath, clock: config.clock}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping operational database: %w", err)
	}
	if current, statErr := os.Lstat(databasePath); statErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(databaseIdentity, current) {
		_ = database.Close()
		return nil, fmt.Errorf("operational database identity changed during open")
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("protect operational database: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *Store) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

func sqliteDSN(path string) string {
	value := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := value.Query()
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(wal)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "synchronous(full)")
	value.RawQuery = query.Encode()
	return value.String()
}

func prepareOperationalDatabase(gitCommon string) (string, fs.FileInfo, error) {
	root, err := pathx.OpenCanonicalRootNoSymlinks(gitCommon)
	if err != nil {
		return "", nil, fmt.Errorf("open Git common directory: %w", err)
	}
	defer root.Close()
	directory, _, err := pathx.EnsureRootAtNoSymlinks(root, "exp/runtime/v1", 0o700)
	if err != nil {
		return "", nil, fmt.Errorf("create operational directory: %w", err)
	}
	defer directory.Close()
	for _, relative := range []string{"exp", "exp/runtime", "exp/runtime/v1"} {
		info, statErr := root.Lstat(relative)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("operational directory %s is not a real directory: %w", relative, statErr)
		}
		if chmodErr := root.Chmod(relative, 0o700); chmodErr != nil {
			return "", nil, fmt.Errorf("protect operational directory %s: %w", relative, chmodErr)
		}
	}
	if file, openErr := directory.OpenFile("control.sqlite", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); openErr == nil {
		if closeErr := file.Close(); closeErr != nil {
			return "", nil, closeErr
		}
		if syncErr := pathx.SyncRoot(directory); syncErr != nil {
			return "", nil, syncErr
		}
	} else if !errors.Is(openErr, fs.ErrExist) {
		return "", nil, fmt.Errorf("create operational database: %w", openErr)
	}
	identity, err := directory.Lstat("control.sqlite")
	if err != nil || identity.Mode()&os.ModeSymlink != 0 || !identity.Mode().IsRegular() {
		return "", nil, fmt.Errorf("operational database is not a regular non-symlink file: %w", err)
	}
	if err := os.Chmod(filepath.Join(gitCommon, filepath.FromSlash(operationalRelative)), 0o600); err != nil {
		return "", nil, fmt.Errorf("protect operational database: %w", err)
	}
	return filepath.Join(gitCommon, filepath.FromSlash(operationalRelative)), identity, nil
}

func (store *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL
		)`,
		`INSERT INTO schema_meta(id, version) VALUES(1, 0) ON CONFLICT(id) DO NOTHING`,
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin operational migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize operational schema: %w", err)
		}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		return fmt.Errorf("read operational schema version: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("operational schema version %d is newer than supported version %d", version, SchemaVersion)
	}
	if version < 1 {
		for _, statement := range schemaV1 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply operational schema v1: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version = 1 WHERE id = 1`); err != nil {
			return fmt.Errorf("record operational schema v1: %w", err)
		}
		version = 1
	}
	if version < 2 {
		for _, statement := range schemaV2 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply operational schema v2: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version = 2 WHERE id = 1`); err != nil {
			return fmt.Errorf("record operational schema v2: %w", err)
		}
		version = 2
	}
	if version < 3 {
		for _, statement := range schemaV3 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply operational schema v3: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version = 3 WHERE id = 1`); err != nil {
			return fmt.Errorf("record operational schema v3: %w", err)
		}
		version = 3
	}
	if version < 4 {
		for _, statement := range schemaV4 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply operational schema v4: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version = 4 WHERE id = 1`); err != nil {
			return fmt.Errorf("record operational schema v4: %w", err)
		}
		version = 4
	}
	if version < 5 {
		for _, statement := range schemaV5 {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply operational schema v5: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_meta SET version = 5 WHERE id = 1`); err != nil {
			return fmt.Errorf("record operational schema v5: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operational migration: %w", err)
	}
	return nil
}

var schemaV1 = []string{
	`CREATE TABLE operations (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL UNIQUE,
		snapshot_digest TEXT NOT NULL,
		payload_json BLOB NOT NULL,
		state TEXT NOT NULL,
		result_json BLOB NOT NULL DEFAULT '{}',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE leases (
		subject TEXT PRIMARY KEY,
		holder TEXT NOT NULL,
		fencing_token INTEGER NOT NULL,
		expires_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		idempotency_key TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL,
		role TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		pool TEXT NOT NULL,
		lane TEXT NOT NULL,
		profile TEXT NOT NULL,
		payload_json BLOB NOT NULL,
		state TEXT NOT NULL,
		claimed_by TEXT NOT NULL DEFAULT '',
		fencing_token INTEGER NOT NULL DEFAULT 0,
		attempt_count INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL,
		lease_expires_at TEXT,
		pueue_task_id INTEGER,
		mlflow_run_id TEXT NOT NULL DEFAULT '',
		result_json BLOB NOT NULL DEFAULT '{}',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX jobs_ready_idx ON jobs(state, pool, lane, created_at, id)`,
	`CREATE TABLE outbox (
		id TEXT PRIMARY KEY,
		operation_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		idempotency_key TEXT NOT NULL UNIQUE,
		payload_json BLOB NOT NULL,
		state TEXT NOT NULL,
		attempt_count INTEGER NOT NULL DEFAULT 0,
		next_attempt_at TEXT NOT NULL,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX outbox_ready_idx ON outbox(state, next_attempt_at, created_at, id)`,
	`CREATE TABLE provider_observations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		context_name TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		observed_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		partial BOOLEAN NOT NULL,
		payload_json BLOB NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX provider_observation_lookup ON provider_observations(provider, context_name, subject_id, observed_at DESC)`,
	`CREATE TABLE fairness (
		pool TEXT PRIMARY KEY,
		exploit_units REAL NOT NULL DEFAULT 0,
		explore_units REAL NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL UNIQUE,
		event_type TEXT NOT NULL,
		aggregate_type TEXT NOT NULL,
		aggregate_id TEXT NOT NULL,
		payload_json BLOB NOT NULL,
		created_at TEXT NOT NULL,
		previous_hash TEXT NOT NULL,
		event_hash TEXT NOT NULL UNIQUE
	)`,
}

var schemaV2 = []string{
	`CREATE TABLE runtime_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		paused BOOLEAN NOT NULL,
		reason TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`INSERT INTO runtime_state(id,paused,reason,updated_at) VALUES(1,0,'','1970-01-01T00:00:00Z')`,
}

var schemaV3 = []string{
	`ALTER TABLE jobs ADD COLUMN units INTEGER NOT NULL DEFAULT 1`,
}

var schemaV4 = []string{
	`ALTER TABLE jobs ADD COLUMN canonical_reconciled BOOLEAN NOT NULL DEFAULT 0`,
	`ALTER TABLE jobs ADD COLUMN canonical_scope TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX jobs_reconcile_idx ON jobs(canonical_scope,canonical_reconciled,state,updated_at,id)`,
	`CREATE UNIQUE INDEX jobs_pueue_task_unique ON jobs(pueue_task_id) WHERE pueue_task_id IS NOT NULL`,
}

var schemaV5 = []string{
	`ALTER TABLE jobs ADD COLUMN fairness_accounted BOOLEAN NOT NULL DEFAULT 0`,
}

func (store *Store) BeginOperation(ctx context.Context, input OperationInput) (Operation, bool, error) {
	if err := validateOperationInput(input); err != nil {
		return Operation{}, false, err
	}
	payload, err := normalizeJSON(input.Payload)
	if err != nil {
		return Operation{}, false, err
	}
	input.Payload = payload
	now := utc(store.clock())
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO operations(
		id, kind, subject_id, idempotency_key, snapshot_digest, payload_json, state, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(idempotency_key) DO NOTHING`,
		input.ID, input.Kind, input.SubjectID, input.IdempotencyKey, input.SnapshotDigest, []byte(payload), OperationPending, formatTime(now), formatTime(now))
	if err != nil {
		return Operation{}, false, fmt.Errorf("create operation: %w", err)
	}
	rows, _ := result.RowsAffected()
	operation, err := operationByKeyTx(ctx, tx, input.IdempotencyKey)
	if err != nil {
		return Operation{}, false, err
	}
	if rows == 0 && !sameOperationInput(operation.OperationInput, input) {
		return operation, true, fmt.Errorf("idempotency key %s belongs to a different operation: %w", input.IdempotencyKey, ErrConflict)
	}
	if rows > 0 {
		if err := appendEventTx(ctx, tx, now, "operation.created", "operation", operation.ID, payload); err != nil {
			return Operation{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Operation{}, false, fmt.Errorf("commit operation: %w", err)
	}
	return operation, rows == 0, nil
}

func (store *Store) SetOperationState(ctx context.Context, id string, state OperationState, result json.RawMessage, message string) (Operation, error) {
	if err := validateIdentifier("operation id", id); err != nil {
		return Operation{}, err
	}
	if !state.valid() {
		return Operation{}, fmt.Errorf("invalid operation state %q", state)
	}
	normalized, err := normalizeJSON(result)
	if err != nil {
		return Operation{}, err
	}
	now := utc(store.clock())
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `UPDATE operations SET state=?, result_json=?, error=?, updated_at=? WHERE id=?`, state, []byte(normalized), message, formatTime(now), id)
	if err != nil {
		return Operation{}, fmt.Errorf("update operation: %w", err)
	}
	if rows, _ := updated.RowsAffected(); rows != 1 {
		return Operation{}, ErrNotFound
	}
	operation, err := operationByIDTx(ctx, tx, id)
	if err != nil {
		return Operation{}, err
	}
	eventPayload, _ := json.Marshal(map[string]any{"state": state, "result": json.RawMessage(normalized), "error": message})
	if err := appendEventTx(ctx, tx, now, "operation.state", "operation", id, eventPayload); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func (store *Store) GetOperationByKey(ctx context.Context, key string) (Operation, error) {
	if err := validateIdentifier("idempotency key", key); err != nil {
		return Operation{}, err
	}
	return operationByKeyQuery(ctx, store.db, key)
}

func (store *Store) AcquireLease(ctx context.Context, subject, holder string, ttl time.Duration) (Lease, error) {
	if err := validateIdentifier("lease subject", subject); err != nil {
		return Lease{}, err
	}
	if err := validateIdentifier("lease holder", holder); err != nil {
		return Lease{}, err
	}
	if ttl <= 0 {
		return Lease{}, errors.New("lease ttl must be positive")
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Lease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentHolder, currentExpires string
	var token int64
	err = tx.QueryRowContext(ctx, `SELECT holder, fencing_token, expires_at FROM leases WHERE subject=?`, subject).Scan(&currentHolder, &token, &currentExpires)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Lease{}, fmt.Errorf("read lease: %w", err)
	}
	if err == nil {
		parsed, parseErr := parseTime(currentExpires)
		if parseErr != nil {
			return Lease{}, parseErr
		}
		if parsed.After(now) && currentHolder != holder {
			return Lease{}, ErrLeaseHeld
		}
	}
	token++
	_, err = tx.ExecContext(ctx, `INSERT INTO leases(subject, holder, fencing_token, expires_at, updated_at)
		VALUES(?, ?, ?, ?, ?) ON CONFLICT(subject) DO UPDATE SET
		holder=excluded.holder, fencing_token=excluded.fencing_token, expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		subject, holder, token, formatTime(expires), formatTime(now))
	if err != nil {
		return Lease{}, fmt.Errorf("write lease: %w", err)
	}
	lease := Lease{Subject: subject, Holder: holder, FencingToken: token, ExpiresAt: expires, UpdatedAt: now}
	payload, _ := json.Marshal(lease)
	if err := appendEventTx(ctx, tx, now, "lease.acquired", "lease", subject, payload); err != nil {
		return Lease{}, err
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (store *Store) RenewLease(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	if ttl <= 0 {
		return Lease{}, errors.New("lease ttl must be positive")
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	result, err := store.db.ExecContext(ctx, `UPDATE leases SET expires_at=?, updated_at=? WHERE subject=? AND holder=? AND fencing_token=?`,
		formatTime(expires), formatTime(now), lease.Subject, lease.Holder, lease.FencingToken)
	if err != nil {
		return Lease{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return Lease{}, ErrFenced
	}
	lease.ExpiresAt = expires
	lease.UpdatedAt = now
	return lease, nil
}

// WithDispatchLease linearizes scheduler submission with daemon leadership and
// SetPaused. The write transaction fences and renews the exact lease before it
// checks pause, then stays open through the single external side effect. Thus a
// successful pause cannot be followed by a submission that was authorized
// from stale state, and an expired former leader cannot keep dispatching.
func (store *Store) WithDispatchLease(ctx context.Context, lease Lease, ttl time.Duration, action DispatchAction) (int64, Lease, error) {
	if ttl <= 0 || action == nil {
		return 0, Lease{}, errors.New("dispatch lease ttl and action are required")
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, Lease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE leases SET expires_at=?,updated_at=?
		WHERE subject=? AND holder=? AND fencing_token=? AND expires_at>?`,
		formatTime(expires), formatTime(now), lease.Subject, lease.Holder, lease.FencingToken, formatTime(now))
	if err != nil {
		return 0, Lease{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return 0, Lease{}, ErrFenced
	}
	var paused bool
	if err := tx.QueryRowContext(ctx, `SELECT paused FROM runtime_state WHERE id=1`).Scan(&paused); err != nil {
		return 0, Lease{}, err
	}
	if paused {
		return 0, Lease{}, ErrPaused
	}
	taskID, err := action()
	if err != nil {
		return 0, Lease{}, err
	}
	if err := tx.Commit(); err != nil {
		return 0, Lease{}, err
	}
	lease.ExpiresAt = expires
	lease.UpdatedAt = now
	return taskID, lease, nil
}

func (store *Store) ReleaseLease(ctx context.Context, lease Lease) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM leases WHERE subject=? AND holder=? AND fencing_token=?`, lease.Subject, lease.Holder, lease.FencingToken)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrFenced
	}
	return nil
}

func (store *Store) EnqueueJob(ctx context.Context, input JobInput) (Job, bool, error) {
	if err := validateJobInput(input); err != nil {
		return Job{}, false, err
	}
	payload, err := normalizeJSON(input.Payload)
	if err != nil {
		return Job{}, false, err
	}
	input.Payload = payload
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 1
	}
	if input.Units == 0 {
		input.Units = 1
	}
	now := utc(store.clock())
	result, err := store.db.ExecContext(ctx, `INSERT INTO jobs(
		id,idempotency_key,kind,role,subject_id,canonical_scope,pool,lane,units,profile,payload_json,state,max_attempts,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`,
		input.ID, input.IdempotencyKey, input.Kind, input.Role, input.SubjectID, input.CanonicalScope, input.Pool, input.Lane, input.Units, input.Profile,
		[]byte(payload), JobQueued, input.MaxAttempts, formatTime(now), formatTime(now))
	if err != nil {
		return Job{}, false, fmt.Errorf("enqueue job: %w", err)
	}
	rows, _ := result.RowsAffected()
	job, err := jobByKeyQuery(ctx, store.db, input.IdempotencyKey)
	if err == nil && !sameJobInput(job.JobInput, input) {
		err = fmt.Errorf("idempotency key %s belongs to a different job: %w", input.IdempotencyKey, ErrConflict)
	}
	return job, rows == 0, err
}

func (store *Store) ClaimJob(ctx context.Context, pool, lane, holder string, ttl time.Duration) (Job, error) {
	for name, value := range map[string]string{"pool": pool, "lane": lane, "holder": holder} {
		if err := validateIdentifier(name, value); err != nil {
			return Job{}, err
		}
	}
	if ttl <= 0 {
		return Job{}, errors.New("job lease ttl must be positive")
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE pool=? AND lane=? AND state=? AND attempt_count < max_attempts ORDER BY created_at,id LIMIT 1`, pool, lane, JobQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, claimed_by=?, fencing_token=fencing_token+1,
		attempt_count=attempt_count+1, lease_expires_at=?, updated_at=? WHERE id=? AND state=?`,
		JobRunning, holder, formatTime(expires), formatTime(now), id, JobQueued)
	if err != nil {
		return Job{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return Job{}, ErrConflict
	}
	job, err := jobByIDTx(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// ClaimJobByID claims exactly one queued job. It exists to prevent a caller
// that just enqueued selection A from accidentally claiming older selection B
// in the same pool/lane.
func (store *Store) ClaimJobByID(ctx context.Context, id, holder string, ttl time.Duration) (Job, error) {
	if err := validateIdentifier("job id", id); err != nil {
		return Job{}, err
	}
	if err := validateIdentifier("holder", holder); err != nil {
		return Job{}, err
	}
	if ttl <= 0 {
		return Job{}, errors.New("job lease ttl must be positive")
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, claimed_by=?, fencing_token=fencing_token+1,
		attempt_count=attempt_count+1, lease_expires_at=?, updated_at=?
		WHERE id=? AND state=? AND attempt_count < max_attempts`, JobRunning, holder, formatTime(expires), formatTime(now), id, JobQueued)
	if err != nil {
		return Job{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		if _, lookupErr := jobByIDTx(ctx, tx, id); lookupErr != nil {
			return Job{}, lookupErr
		}
		return Job{}, ErrConflict
	}
	job, err := jobByIDTx(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// PrepareSubmission atomically enqueues and claims one exact job and publishes
// its outbox intent. There is no durable state in which a claimed job lacks the
// scheduler intent needed for crash recovery.
func (store *Store) PrepareSubmission(ctx context.Context, input JobInput, holder string, ttl time.Duration, factory OutboxFactory) (Job, OutboxItem, bool, error) {
	if err := validateJobInput(input); err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	if err := validateIdentifier("holder", holder); err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	if ttl <= 0 {
		return Job{}, OutboxItem{}, false, errors.New("job lease ttl must be positive")
	}
	if factory == nil {
		return Job{}, OutboxItem{}, false, errors.New("outbox factory is required")
	}
	payload, err := normalizeJSON(input.Payload)
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	input.Payload = payload
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 1
	}
	if input.Units == 0 {
		input.Units = 1
	}
	now := utc(store.clock())
	expires := utc(now.Add(ttl))
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := tx.ExecContext(ctx, `INSERT INTO jobs(
		id,idempotency_key,kind,role,subject_id,canonical_scope,pool,lane,units,profile,payload_json,state,max_attempts,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`,
		input.ID, input.IdempotencyKey, input.Kind, input.Role, input.SubjectID, input.CanonicalScope, input.Pool, input.Lane, input.Units, input.Profile,
		[]byte(input.Payload), JobQueued, input.MaxAttempts, formatTime(now), formatTime(now))
	if err != nil {
		return Job{}, OutboxItem{}, false, fmt.Errorf("enqueue submission job: %w", err)
	}
	rows, _ := inserted.RowsAffected()
	job, err := jobByKeyTx(ctx, tx, input.IdempotencyKey)
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	if !sameJobInput(job.JobInput, input) {
		return Job{}, OutboxItem{}, false, fmt.Errorf("idempotency key %s belongs to a different job: %w", input.IdempotencyKey, ErrConflict)
	}
	switch job.State {
	case JobQueued:
		claimed, claimErr := tx.ExecContext(ctx, `UPDATE jobs SET state=?, claimed_by=?, fencing_token=fencing_token+1,
			attempt_count=attempt_count+1, lease_expires_at=?, updated_at=?
			WHERE id=? AND state=? AND attempt_count < max_attempts`, JobRunning, holder, formatTime(expires), formatTime(now), job.ID, JobQueued)
		if claimErr != nil {
			return Job{}, OutboxItem{}, false, claimErr
		}
		if claimedRows, _ := claimed.RowsAffected(); claimedRows != 1 {
			return Job{}, OutboxItem{}, false, ErrConflict
		}
		job, err = jobByIDTx(ctx, tx, job.ID)
		if err != nil {
			return Job{}, OutboxItem{}, false, err
		}
	case JobRunning:
		if job.FencingToken <= 0 {
			return Job{}, OutboxItem{}, false, ErrConflict
		}
	default:
		return Job{}, OutboxItem{}, false, fmt.Errorf("job %s is %s: %w", job.ID, job.State, ErrConflict)
	}
	outboxInput, err := factory(job)
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	for name, value := range map[string]string{"outbox id": outboxInput.ID, "operation id": outboxInput.OperationID, "kind": outboxInput.Kind, "idempotency key": outboxInput.IdempotencyKey} {
		if err := validateIdentifier(name, value); err != nil {
			return Job{}, OutboxItem{}, false, err
		}
	}
	outboxPayload, err := normalizeJSON(outboxInput.Payload)
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	outboxInput.Payload = outboxPayload
	outboxInserted, err := tx.ExecContext(ctx, `INSERT INTO outbox(id,operation_id,kind,idempotency_key,payload_json,state,next_attempt_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, outboxInput.ID, outboxInput.OperationID, outboxInput.Kind,
		outboxInput.IdempotencyKey, []byte(outboxInput.Payload), OutboxPending, formatTime(now), formatTime(now), formatTime(now))
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	item, err := outboxByKeyTx(ctx, tx, outboxInput.IdempotencyKey)
	if err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	if item.OperationID != job.ID || item.Kind != outboxInput.Kind || string(item.Payload) != string(outboxInput.Payload) {
		return Job{}, OutboxItem{}, false, fmt.Errorf("outbox idempotency key belongs to a different intent: %w", ErrConflict)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, OutboxItem{}, false, err
	}
	outboxRows, _ := outboxInserted.RowsAffected()
	return job, item, rows == 0 && outboxRows == 0, nil
}

func (store *Store) FinishJob(ctx context.Context, id string, token int64, state JobState, result json.RawMessage, message string) (Job, error) {
	if !state.terminal() && state != JobUnknown {
		return Job{}, fmt.Errorf("job finish requires terminal or unknown state, got %q", state)
	}
	normalized, err := normalizeJSON(result)
	if err != nil {
		return Job{}, err
	}
	now := utc(store.clock())
	updated, err := store.db.ExecContext(ctx, `UPDATE jobs SET state=?, result_json=?, error=?, lease_expires_at=NULL, updated_at=?
		WHERE id=? AND fencing_token=? AND state=?`, state, []byte(normalized), message, formatTime(now), id, token, JobRunning)
	if err != nil {
		return Job{}, err
	}
	if rows, _ := updated.RowsAffected(); rows != 1 {
		existing, lookupErr := jobByIDQuery(ctx, store.db, id)
		if lookupErr == nil && existing.FencingToken == token && existing.State == state && string(existing.Result) == string(normalized) {
			return existing, nil
		}
		return Job{}, ErrFenced
	}
	return jobByIDQuery(ctx, store.db, id)
}

func (store *Store) SetJobExternalRefs(ctx context.Context, id string, token int64, pueueTaskID *int64, mlflowRunID string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE jobs SET pueue_task_id=?, mlflow_run_id=?, updated_at=? WHERE id=? AND fencing_token=?`,
		pueueTaskID, mlflowRunID, formatTime(utc(store.clock())), id, token)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrFenced
	}
	return nil
}

func (store *Store) ListJobs(ctx context.Context, states ...JobState) ([]Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs`
	arguments := make([]any, 0, len(states))
	if len(states) > 0 {
		placeholders := make([]string, len(states))
		for index, state := range states {
			if !state.valid() {
				return nil, fmt.Errorf("invalid job state %q", state)
			}
			placeholders[index] = "?"
			arguments = append(arguments, state)
		}
		query += ` WHERE state IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY created_at,id`
	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows.Scan)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if jobs == nil {
		jobs = []Job{}
	}
	return jobs, rows.Err()
}

// ListUnreconciledTerminalJobs pages only terminal results not yet imported to
// canonical Attempt records. Replays are safe until MarkJobReconciled succeeds.
func (store *Store) ListUnreconciledTerminalJobs(ctx context.Context, scope string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("terminal reconciliation limit is outside 1..1000")
	}
	if scope != "" {
		if err := validateIdentifier("canonical scope", scope); err != nil {
			return nil, err
		}
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs
		WHERE canonical_reconciled=0 AND canonical_scope=? AND kind='experiment.run' AND role='execute' AND state IN (?,?,?) ORDER BY updated_at,id LIMIT ?`,
		scope, JobSucceeded, JobFailed, JobCancelled, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		job, scanErr := scanJob(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (store *Store) MarkJobReconciled(ctx context.Context, id string, token int64) error {
	if err := validateIdentifier("job id", id); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE jobs SET canonical_reconciled=1
		WHERE id=? AND fencing_token=? AND state IN (?,?,?)`, id, token, JobSucceeded, JobFailed, JobCancelled)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrFenced
	}
	return nil
}

func (store *Store) ListActiveAllocations(ctx context.Context) ([]ActiveAllocation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT pueue_task_id,pool,units FROM jobs
		WHERE state IN (?,?) AND pueue_task_id IS NOT NULL ORDER BY pueue_task_id`, JobQueued, JobRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allocations := []ActiveAllocation{}
	for rows.Next() {
		var allocation ActiveAllocation
		if err := rows.Scan(&allocation.PueueTaskID, &allocation.Pool, &allocation.Units); err != nil {
			return nil, err
		}
		allocations = append(allocations, allocation)
	}
	return allocations, rows.Err()
}

func (store *Store) GetJob(ctx context.Context, id string) (Job, error) {
	if err := validateIdentifier("job id", id); err != nil {
		return Job{}, err
	}
	return jobByIDQuery(ctx, store.db, id)
}

func (store *Store) AddOutbox(ctx context.Context, input OutboxInput, next time.Time) (OutboxItem, bool, error) {
	for name, value := range map[string]string{"outbox id": input.ID, "operation id": input.OperationID, "kind": input.Kind, "idempotency key": input.IdempotencyKey} {
		if err := validateIdentifier(name, value); err != nil {
			return OutboxItem{}, false, err
		}
	}
	payload, err := normalizeJSON(input.Payload)
	if err != nil {
		return OutboxItem{}, false, err
	}
	input.Payload = payload
	now := utc(store.clock())
	if next.IsZero() {
		next = now
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO outbox(id,operation_id,kind,idempotency_key,payload_json,state,next_attempt_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, input.ID, input.OperationID, input.Kind, input.IdempotencyKey,
		[]byte(payload), OutboxPending, formatTime(utc(next)), formatTime(now), formatTime(now))
	if err != nil {
		return OutboxItem{}, false, err
	}
	rows, _ := result.RowsAffected()
	item, err := outboxByKeyQuery(ctx, store.db, input.IdempotencyKey)
	if err == nil && rows == 0 && !sameOutboxInput(item.OutboxInput, input) {
		err = fmt.Errorf("idempotency key %s belongs to a different outbox intent: %w", input.IdempotencyKey, ErrConflict)
	}
	return item, rows == 0, err
}

func (store *Store) DueOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE state IN (?,?) AND next_attempt_at<=? ORDER BY created_at,id LIMIT ?`,
		OutboxPending, OutboxFailed, formatTime(utc(store.clock())), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OutboxItem{}
	for rows.Next() {
		item, err := scanOutbox(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// DueOutboxForScope returns scheduler intents owned by one canonical checkout.
// The JOIN applies scope before LIMIT, preventing another worktree's backlog
// from causing head-of-line blocking or accidental submission.
func (store *Store) DueOutboxForScope(ctx context.Context, scope string, limit int) ([]OutboxItem, error) {
	if err := validateIdentifier("canonical scope", scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	const columns = `o.id,o.operation_id,o.kind,o.idempotency_key,o.payload_json,o.state,o.attempt_count,o.next_attempt_at,o.last_error,o.created_at,o.updated_at`
	rows, err := store.db.QueryContext(ctx, `SELECT `+columns+` FROM outbox o JOIN jobs j ON j.id=o.operation_id
		WHERE j.canonical_scope=? AND o.state IN (?,?) AND o.next_attempt_at<=? ORDER BY o.created_at,o.id LIMIT ?`,
		scope, OutboxPending, OutboxFailed, formatTime(utc(store.clock())), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OutboxItem{}
	for rows.Next() {
		item, scanErr := scanOutbox(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *Store) SetOutboxState(ctx context.Context, id string, state OutboxState, retryAt time.Time, message string) error {
	if state != OutboxRunning && state != OutboxSucceeded && state != OutboxFailed {
		return fmt.Errorf("invalid outbox state %q", state)
	}
	now := utc(store.clock())
	if retryAt.IsZero() {
		retryAt = now
	}
	result, err := store.db.ExecContext(ctx, `UPDATE outbox SET state=?, attempt_count=attempt_count+1, next_attempt_at=?, last_error=?, updated_at=? WHERE id=?`,
		state, formatTime(utc(retryAt)), message, formatTime(now), id)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) RecordObservation(ctx context.Context, input ObservationInput) (Observation, error) {
	for name, value := range map[string]string{"provider": input.Provider, "context": input.Context, "subject": input.SubjectID} {
		if err := validateIdentifier(name, value); err != nil {
			return Observation{}, err
		}
	}
	payload, err := normalizeJSON(input.Payload)
	if err != nil {
		return Observation{}, err
	}
	input.Payload = payload
	input.ObservedAt = utc(input.ObservedAt)
	input.ExpiresAt = utc(input.ExpiresAt)
	if input.ObservedAt.IsZero() {
		input.ObservedAt = utc(store.clock())
	}
	if input.ExpiresAt.Before(input.ObservedAt) {
		return Observation{}, errors.New("observation expiry precedes observation time")
	}
	created := utc(store.clock())
	result, err := store.db.ExecContext(ctx, `INSERT INTO provider_observations(provider,context_name,subject_id,observed_at,expires_at,partial,payload_json,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, input.Provider, input.Context, input.SubjectID, formatTime(input.ObservedAt), formatTime(input.ExpiresAt), input.Partial, []byte(payload), formatTime(created))
	if err != nil {
		return Observation{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Observation{}, err
	}
	return Observation{ID: id, ObservationInput: input, CreatedAt: created}, nil
}

func (store *Store) RecordDispatch(ctx context.Context, pool, lane string, units float64) (Fairness, error) {
	if lane != "exploit" && lane != "explore" {
		return Fairness{}, fmt.Errorf("invalid dispatch lane %q", lane)
	}
	if units <= 0 {
		return Fairness{}, errors.New("dispatch units must be positive")
	}
	now := utc(store.clock())
	exploit, explore := 0.0, 0.0
	if lane == "exploit" {
		exploit = units
	} else {
		explore = units
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO fairness(pool,exploit_units,explore_units,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(pool) DO UPDATE SET exploit_units=exploit_units+excluded.exploit_units,
		explore_units=explore_units+excluded.explore_units,updated_at=excluded.updated_at`, pool, exploit, explore, formatTime(now))
	if err != nil {
		return Fairness{}, err
	}
	return store.Fairness(ctx, pool)
}

// RecordDispatchOnce accounts a durable job at most once, including across a
// crash between scheduler submission and controller completion.
func (store *Store) RecordDispatchOnce(ctx context.Context, jobID, pool, lane string, units float64) (Fairness, error) {
	if err := validateIdentifier("job id", jobID); err != nil {
		return Fairness{}, err
	}
	if err := validateIdentifier("pool", pool); err != nil {
		return Fairness{}, err
	}
	if lane != "exploit" && lane != "explore" {
		return Fairness{}, fmt.Errorf("invalid dispatch lane %q", lane)
	}
	if units <= 0 {
		return Fairness{}, errors.New("dispatch units must be positive")
	}
	now := utc(store.clock())
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Fairness{}, err
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `UPDATE jobs SET fairness_accounted=1 WHERE id=? AND fairness_accounted=0`, jobID)
	if err != nil {
		return Fairness{}, err
	}
	rows, _ := updated.RowsAffected()
	if rows == 0 {
		var accounted bool
		if err := tx.QueryRowContext(ctx, `SELECT fairness_accounted FROM jobs WHERE id=?`, jobID).Scan(&accounted); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Fairness{}, ErrNotFound
			}
			return Fairness{}, err
		}
		if !accounted {
			return Fairness{}, ErrConflict
		}
	} else {
		exploit, explore := 0.0, 0.0
		if lane == "exploit" {
			exploit = units
		} else {
			explore = units
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fairness(pool,exploit_units,explore_units,updated_at) VALUES(?,?,?,?)
			ON CONFLICT(pool) DO UPDATE SET exploit_units=exploit_units+excluded.exploit_units,
			explore_units=explore_units+excluded.explore_units,updated_at=excluded.updated_at`, pool, exploit, explore, formatTime(now)); err != nil {
			return Fairness{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Fairness{}, err
	}
	return store.Fairness(ctx, pool)
}

func (store *Store) Fairness(ctx context.Context, pool string) (Fairness, error) {
	var result Fairness
	var updated string
	err := store.db.QueryRowContext(ctx, `SELECT pool,exploit_units,explore_units,updated_at FROM fairness WHERE pool=?`, pool).
		Scan(&result.Pool, &result.ExploitUnits, &result.ExploreUnits, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Fairness{Pool: pool}, nil
	}
	if err != nil {
		return Fairness{}, err
	}
	result.UpdatedAt, err = parseTime(updated)
	return result, err
}

func (store *Store) SetPaused(ctx context.Context, paused bool, reason string) (RuntimeState, error) {
	if len(reason) > 4096 || strings.ContainsRune(reason, 0) {
		return RuntimeState{}, errors.New("runtime pause reason is invalid")
	}
	now := utc(store.clock())
	_, err := store.db.ExecContext(ctx, `UPDATE runtime_state SET paused=?,reason=?,updated_at=? WHERE id=1`, paused, reason, formatTime(now))
	if err != nil {
		return RuntimeState{}, err
	}
	return RuntimeState{Paused: paused, Reason: reason, UpdatedAt: now}, nil
}

func (store *Store) RuntimeState(ctx context.Context) (RuntimeState, error) {
	var result RuntimeState
	var updated string
	if err := store.db.QueryRowContext(ctx, `SELECT paused,reason,updated_at FROM runtime_state WHERE id=1`).Scan(&result.Paused, &result.Reason, &updated); err != nil {
		return RuntimeState{}, err
	}
	var err error
	result.UpdatedAt, err = parseTime(updated)
	return result, err
}

func validateOperationInput(input OperationInput) error {
	for name, value := range map[string]string{"operation id": input.ID, "kind": input.Kind, "subject id": input.SubjectID, "idempotency key": input.IdempotencyKey} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if input.SnapshotDigest != "" && !strings.HasPrefix(input.SnapshotDigest, "sha256:") {
		return errors.New("snapshot digest must use sha256 prefix")
	}
	return nil
}

func validateJobInput(input JobInput) error {
	for name, value := range map[string]string{"job id": input.ID, "idempotency key": input.IdempotencyKey, "kind": input.Kind, "role": input.Role, "subject id": input.SubjectID, "pool": input.Pool, "lane": input.Lane, "profile": input.Profile} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if input.CanonicalScope != "" {
		if err := validateIdentifier("canonical scope", input.CanonicalScope); err != nil {
			return err
		}
	}
	if input.Lane != "exploit" && input.Lane != "explore" {
		return fmt.Errorf("invalid job lane %q", input.Lane)
	}
	if input.MaxAttempts < 0 || input.MaxAttempts > 100 {
		return errors.New("max attempts is outside 1..100")
	}
	if input.Units < 0 || input.Units > 1_000_000 {
		return errors.New("job units are outside 1..1000000")
	}
	return nil
}

type scanner func(...any) error

const operationColumns = `id,kind,subject_id,idempotency_key,snapshot_digest,payload_json,state,result_json,error,created_at,updated_at`

func operationByKeyTx(ctx context.Context, tx *sql.Tx, key string) (Operation, error) {
	return scanOperation(tx.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations WHERE idempotency_key=?`, key).Scan)
}

func operationByIDTx(ctx context.Context, tx *sql.Tx, id string) (Operation, error) {
	return scanOperation(tx.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations WHERE id=?`, id).Scan)
}

func operationByKeyQuery(ctx context.Context, db *sql.DB, key string) (Operation, error) {
	return scanOperation(db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations WHERE idempotency_key=?`, key).Scan)
}

func scanOperation(scan scanner) (Operation, error) {
	var value Operation
	var payload, result []byte
	var created, updated string
	if err := scan(&value.ID, &value.Kind, &value.SubjectID, &value.IdempotencyKey, &value.SnapshotDigest, &payload,
		&value.State, &result, &value.Error, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Operation{}, ErrNotFound
		}
		return Operation{}, err
	}
	value.Payload, value.Result = payload, result
	var err error
	value.CreatedAt, err = parseTime(created)
	if err == nil {
		value.UpdatedAt, err = parseTime(updated)
	}
	return value, err
}

const jobColumns = `id,idempotency_key,kind,role,subject_id,canonical_scope,pool,lane,units,profile,payload_json,state,claimed_by,fencing_token,attempt_count,max_attempts,lease_expires_at,pueue_task_id,mlflow_run_id,result_json,error,created_at,updated_at`

func jobByKeyQuery(ctx context.Context, db *sql.DB, key string) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key=?`, key).Scan)
}

func jobByIDQuery(ctx context.Context, db *sql.DB, id string) (Job, error) {
	return scanJob(db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id).Scan)
}

func jobByIDTx(ctx context.Context, tx *sql.Tx, id string) (Job, error) {
	return scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id).Scan)
}

func jobByKeyTx(ctx context.Context, tx *sql.Tx, key string) (Job, error) {
	return scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key=?`, key).Scan)
}

func scanJob(scan scanner) (Job, error) {
	var value Job
	var payload, result []byte
	var lease, created, updated sql.NullString
	var taskID sql.NullInt64
	if err := scan(&value.ID, &value.IdempotencyKey, &value.Kind, &value.Role, &value.SubjectID, &value.CanonicalScope, &value.Pool, &value.Lane,
		&value.Units, &value.Profile, &payload, &value.State, &value.ClaimedBy, &value.FencingToken, &value.AttemptCount, &value.MaxAttempts,
		&lease, &taskID, &value.MLflowRunID, &result, &value.Error, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	value.Payload, value.Result = payload, result
	if taskID.Valid {
		value.PueueTaskID = &taskID.Int64
	}
	var err error
	if created.Valid {
		value.CreatedAt, err = parseTime(created.String)
	}
	if err == nil && updated.Valid {
		value.UpdatedAt, err = parseTime(updated.String)
	}
	if err == nil && lease.Valid {
		parsed, parseErr := parseTime(lease.String)
		err = parseErr
		if parseErr == nil {
			value.LeaseExpiresAt = &parsed
		}
	}
	return value, err
}

const outboxColumns = `id,operation_id,kind,idempotency_key,payload_json,state,attempt_count,next_attempt_at,last_error,created_at,updated_at`

func outboxByKeyQuery(ctx context.Context, db *sql.DB, key string) (OutboxItem, error) {
	return scanOutbox(db.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE idempotency_key=?`, key).Scan)
}

func outboxByKeyTx(ctx context.Context, tx *sql.Tx, key string) (OutboxItem, error) {
	return scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE idempotency_key=?`, key).Scan)
}

func sameJobInput(left, right JobInput) bool {
	return left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey && left.Kind == right.Kind &&
		left.Role == right.Role && left.SubjectID == right.SubjectID && left.CanonicalScope == right.CanonicalScope && left.Pool == right.Pool &&
		left.Lane == right.Lane && left.Units == right.Units && left.Profile == right.Profile && left.MaxAttempts == right.MaxAttempts &&
		string(left.Payload) == string(right.Payload)
}

func sameOperationInput(left, right OperationInput) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.SubjectID == right.SubjectID &&
		left.IdempotencyKey == right.IdempotencyKey && left.SnapshotDigest == right.SnapshotDigest &&
		string(left.Payload) == string(right.Payload)
}

func sameOutboxInput(left, right OutboxInput) bool {
	return left.ID == right.ID && left.OperationID == right.OperationID && left.Kind == right.Kind &&
		left.IdempotencyKey == right.IdempotencyKey && string(left.Payload) == string(right.Payload)
}

func scanOutbox(scan scanner) (OutboxItem, error) {
	var value OutboxItem
	var payload []byte
	var next, created, updated string
	if err := scan(&value.ID, &value.OperationID, &value.Kind, &value.IdempotencyKey, &payload, &value.State,
		&value.AttemptCount, &next, &value.LastError, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OutboxItem{}, ErrNotFound
		}
		return OutboxItem{}, err
	}
	value.Payload = payload
	var err error
	value.NextAttemptAt, err = parseTime(next)
	if err == nil {
		value.CreatedAt, err = parseTime(created)
	}
	if err == nil {
		value.UpdatedAt, err = parseTime(updated)
	}
	return value, err
}

func appendEventTx(ctx context.Context, tx *sql.Tx, now time.Time, eventType, aggregateType, aggregateID string, payload []byte) error {
	var previous string
	err := tx.QueryRowContext(ctx, `SELECT event_hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		previous = ""
	} else if err != nil {
		return err
	}
	idDigest := sha256.Sum256([]byte(previous + "\x00" + eventType + "\x00" + aggregateID + "\x00" + formatTime(now) + "\x00" + string(payload)))
	eventID := eventType + ":" + aggregateID + ":" + hex.EncodeToString(idDigest[:8])
	hash := sha256.New()
	for _, part := range []string{"exp-operation-event-v1", previous, eventID, eventType, aggregateType, aggregateID, formatTime(now), string(payload)} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	eventHash := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	_, err = tx.ExecContext(ctx, `INSERT INTO events(event_id,event_type,aggregate_type,aggregate_id,payload_json,created_at,previous_hash,event_hash)
		VALUES(?,?,?,?,?,?,?,?)`, eventID, eventType, aggregateType, aggregateID, payload, formatTime(now), previous, eventHash)
	return err
}

func formatTime(value time.Time) string { return utc(value).Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse operational timestamp: %w", err)
	}
	return utc(parsed), nil
}

func (store *Store) RemoveIfEmpty() error {
	if store == nil {
		return nil
	}
	jobs, err := store.ListJobs(context.Background(), JobQueued, JobRunning)
	if err != nil {
		return err
	}
	if len(jobs) != 0 {
		return fmt.Errorf("operational database has active jobs: %w", ErrConflict)
	}
	if err := store.Close(); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(store.path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
