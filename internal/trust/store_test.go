package trust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

func TestStoreExactDigestCapabilityAndContextInvalidation(t *testing.T) {
	common := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	projectID := mustTrustProjectID(t, "01a04000-0000-7001-8000-000000000001")
	sourceID := mustTrustSourceID(t, "src_01a04000-0000-7002-8000-000000000002")
	subject := Subject{ProjectID: projectID, SourceID: sourceID, GitCommonDir: common, ConfigScope: "services/api/.exp-cli/config.toml"}
	path := filepath.Join(t.TempDir(), "state", "exp", "trust", "v1.json")
	approvedAt := time.Date(2026, 9, 3, 10, 11, 12, 0, time.FixedZone("test", 8*60*60))
	store := NewStore(WithPath(path), WithClock(func() time.Time { return approvedAt }))
	first := trustDigest('1')
	second := trustDigest('2')

	receipt, err := store.Approve(context.Background(), Query{
		Subject: subject, ConfigDigest: first,
		Capabilities: []Capability{CapabilityAgentProfile, CapabilitySourceSelection},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.ApprovedAt.Equal(approvedAt) || receipt.ApprovedAt.Location() != time.UTC {
		t.Fatalf("approval time = %s (%s)", receipt.ApprovedAt, receipt.ApprovedAt.Location())
	}
	inspection, err := store.Inspect(context.Background(), Query{
		Subject: subject, ConfigDigest: first,
		Capabilities: []Capability{CapabilitySourceSelection, CapabilityAgentProfile},
	})
	if err != nil || !inspection.Trusted || len(inspection.Missing) != 0 {
		t.Fatalf("exact inspection = %#v, %v", inspection, err)
	}
	inspection, err = store.Inspect(context.Background(), Query{
		Subject: subject, ConfigDigest: second,
		Capabilities: []Capability{CapabilitySourceSelection},
	})
	if err != nil || inspection.Trusted || len(inspection.Missing) != 1 {
		t.Fatalf("changed digest inspection = %#v, %v", inspection, err)
	}

	if _, err := store.Approve(context.Background(), Query{
		Subject: subject, ConfigDigest: second,
		Capabilities: []Capability{CapabilitySourceSelection},
	}); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.Check(context.Background(), subject, first, CapabilitySourceSelection); err != nil || trusted {
		t.Fatalf("old digest/source trust = %v, %v", trusted, err)
	}
	if trusted, err := store.Check(context.Background(), subject, first, CapabilityAgentProfile); err != nil || !trusted {
		t.Fatalf("disjoint old capability trust = %v, %v", trusted, err)
	}
	if _, err := store.Approve(context.Background(), Query{
		Subject: subject, ConfigDigest: second,
		Capabilities: []Capability{CapabilityWorkspaceBackend},
	}); err != nil {
		t.Fatal(err)
	}
	receipts, err := store.List(context.Background())
	if err != nil || len(receipts) != 2 {
		t.Fatalf("receipts = %#v, %v", receipts, err)
	}
	if len(receipts[1].Capabilities) != 2 || receipts[1].Capabilities[0] != CapabilitySourceSelection || receipts[1].Capabilities[1] != CapabilityWorkspaceBackend {
		t.Fatalf("coalesced capabilities = %#v", receipts[1].Capabilities)
	}

	otherSubject := subject
	otherSubject.ConfigScope = "other/.exp-cli/config.toml"
	if trusted, err := store.Check(context.Background(), otherSubject, second, CapabilitySourceSelection); err != nil || trusted {
		t.Fatalf("other config scope trust = %v, %v", trusted, err)
	}
	otherCommon := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(otherCommon, 0o755); err != nil {
		t.Fatal(err)
	}
	otherSubject = subject
	otherSubject.GitCommonDir = otherCommon
	if trusted, err := store.Check(context.Background(), otherSubject, second, CapabilitySourceSelection); err != nil || trusted {
		t.Fatalf("other Git-common trust = %v, %v", trusted, err)
	}
	otherSubject = subject
	otherSubject.SourceID = mustTrustSourceID(t, "src_01a04000-0000-7003-8000-000000000003")
	if trusted, err := store.Check(context.Background(), otherSubject, second, CapabilitySourceSelection); err != nil || trusted {
		t.Fatalf("other Source context trust = %v, %v", trusted, err)
	}

	changed, err := store.Revoke(context.Background(), RevokeRequest{
		Subject: subject, ConfigDigest: second,
		Capabilities: []Capability{CapabilitySourceSelection},
	})
	if err != nil || changed != 1 {
		t.Fatalf("selective revoke = %d, %v", changed, err)
	}
	if trusted, _ := store.Check(context.Background(), subject, second, CapabilitySourceSelection); trusted {
		t.Fatal("revoked capability remained trusted")
	}
	if trusted, err := store.Check(context.Background(), subject, second, CapabilityWorkspaceBackend); err != nil || !trusted {
		t.Fatalf("unrevoked capability = %v, %v", trusted, err)
	}
	removed, err := store.RevokeAll(context.Background(), subject)
	if err != nil || !removed {
		t.Fatalf("RevokeAll = %v, %v", removed, err)
	}
	if receipts, err := store.List(context.Background()); err != nil || len(receipts) != 0 {
		t.Fatalf("receipts after revoke = %#v, %v", receipts, err)
	}
}

func TestTrustReceiptDoesNotFollowGitCommonReplacementAtSamePath(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(root, "state", "trust", "v1.json")))
	subject := Subject{ProjectID: mustTrustProjectID(t, "01a04010-0000-7001-8000-000000000001"), GitCommonDir: common, ConfigScope: RelativeConfigScopeForTest}
	digest := trustDigest('9')
	if _, err := store.Approve(context.Background(), Query{Subject: subject, ConfigDigest: digest, Capabilities: []Capability{CapabilityAgentProfile, CapabilityMLflowProfile}}); err != nil {
		t.Fatal(err)
	}
	original := common + "-original"
	if err := os.Rename(common, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.Check(context.Background(), subject, digest, CapabilityAgentProfile); err != nil || trusted {
		t.Fatalf("replacement Git common inherited trust = %t, %v", trusted, err)
	}
	if _, err := store.Approve(context.Background(), Query{Subject: subject, ConfigDigest: digest, Capabilities: []Capability{CapabilityAgentProfile}}); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.Check(context.Background(), subject, digest, CapabilityMLflowProfile); err != nil || trusted {
		t.Fatalf("replacement approval inherited an old capability = %t, %v", trusted, err)
	}
	if changed, err := store.Revoke(context.Background(), RevokeRequest{Subject: subject}); err != nil || changed != 1 {
		t.Fatalf("revoke historical logical subject = %d, %v", changed, err)
	}
	if err := os.Remove(common); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, common); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.Check(context.Background(), subject, digest, CapabilityAgentProfile); err != nil || trusted {
		t.Fatalf("revoked original identity regained trust = %t, %v", trusted, err)
	}
}

func TestProjectOnlyReceiptRoundTripsPrivatelyAndListIsPure(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "repo", ".git")
	if err := os.MkdirAll(common, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "absent-state", "exp", "trust", "v1.json")
	store := NewStore(WithPath(path))
	if records, err := store.List(context.Background()); err != nil || len(records) != 0 {
		t.Fatalf("absent List = %#v, %v", records, err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only List created state: %v", err)
	}
	subject := Subject{
		ProjectID:    mustTrustProjectID(t, "01a04100-0000-7001-8000-000000000001"),
		GitCommonDir: common, ConfigScope: RelativeConfigScopeForTest,
	}
	if _, err := store.Approve(context.Background(), Query{
		Subject: subject, ConfigDigest: trustDigest('a'),
		Capabilities: []Capability{CapabilityMLflowProfile},
	}); err != nil {
		t.Fatal(err)
	}
	records, err := NewStore(WithPath(path)).List(context.Background())
	if err != nil || len(records) != 1 || !records[0].SourceID.IsZero() {
		t.Fatalf("project-only receipt = %#v, %v", records, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"source_id"`) {
		t.Fatalf("zero Source ID should be omitted: %s", data)
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(root, "absent-state"), 0o700},
		{filepath.Join(root, "absent-state", "exp"), 0o700},
		{filepath.Join(root, "absent-state", "exp", "trust"), 0o700},
		{filepath.Join(root, "absent-state", "exp", "trust", "lock"), 0o600},
		{path, 0o600},
	} {
		info, err := os.Stat(check.path)
		if err != nil || info.Mode().Perm() != check.mode {
			t.Errorf("%s mode = %v, %v; want %04o", check.path, info.Mode().Perm(), err, check.mode)
		}
	}
}

const RelativeConfigScopeForTest = ".exp-cli/config.toml"

func TestTrustStoreRecoversPublishedWriteAndRejectsMalformedState(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "trust", "v1.json")
	sentinel := errors.New("injected directory sync failure")
	fired := false
	store := NewStore(
		WithPath(path),
		WithAtomicHook(func(stage record.AtomicStage, _ string) error {
			if stage == record.StageDirSync && !fired {
				fired = true
				return sentinel
			}
			return nil
		}),
	)
	subject := Subject{ProjectID: mustTrustProjectID(t, "01a04200-0000-7001-8000-000000000001"), GitCommonDir: common}
	if _, err := store.Approve(context.Background(), Query{
		Subject: subject, ConfigDigest: trustDigest('b'),
		Capabilities: []Capability{CapabilityAgentProfile},
	}); !errors.Is(err, sentinel) || !fired {
		t.Fatalf("interrupted approve = %v, fired=%v", err, fired)
	}
	if records, err := NewStore(WithPath(path)).List(context.Background()); err != nil || len(records) != 1 {
		t.Fatalf("published receipt recovery = %#v, %v", records, err)
	}

	badPath := filepath.Join(root, "bad", "v1.json")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte(`{"schema":"exp.trust/v1","receipts":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(WithPath(badPath)).List(context.Background()); err == nil {
		t.Fatal("unknown trust store field was accepted")
	}
	if err := os.Chmod(badPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(WithPath(badPath)).List(context.Background()); err == nil {
		t.Fatal("insecure trust store mode was accepted")
	}

	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"schema":"exp.trust/v1","receipts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(root, "links", "v1.json")
	if err := os.MkdirAll(filepath.Dir(symlinkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewStore(WithPath(symlinkPath)).List(context.Background()); err == nil {
		t.Fatal("symlink trust store was accepted")
	}
}

func TestConcurrentStoreInstancesDoNotLoseApprovals(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(root, ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "trust", "v1.json")
	projectID := mustTrustProjectID(t, "01a04400-0000-7001-8000-000000000001")
	const count = 8
	var wait sync.WaitGroup
	errorsByIndex := make([]error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			store := NewStore(WithPath(path))
			_, errorsByIndex[index] = store.Approve(context.Background(), Query{
				Subject: Subject{
					ProjectID: projectID, GitCommonDir: common,
					ConfigScope: fmt.Sprintf("scope-%02d/.exp-cli/config.toml", index),
				},
				ConfigDigest: trustDigest(byte('1' + index)),
				Capabilities: []Capability{CapabilityAgentProfile},
			})
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("approval %d: %v", index, err)
		}
	}
	receipts, err := NewStore(WithPath(path)).List(context.Background())
	if err != nil || len(receipts) != count {
		t.Fatalf("concurrent receipts = %d, %v: %#v", len(receipts), err, receipts)
	}
}

func TestTrustValidationRejectsUnknownCapabilitiesAndBadDigests(t *testing.T) {
	common := filepath.Join(t.TempDir(), ".git")
	if err := os.Mkdir(common, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewStore(WithPath(filepath.Join(t.TempDir(), "trust", "v1.json")))
	subject := Subject{ProjectID: mustTrustProjectID(t, "01a04300-0000-7001-8000-000000000001"), GitCommonDir: common}
	for _, query := range []Query{
		{Subject: subject, ConfigDigest: "bad", Capabilities: []Capability{CapabilityAgentProfile}},
		{Subject: subject, ConfigDigest: trustDigest('c'), Capabilities: []Capability{"shell.hook"}},
		{Subject: subject, ConfigDigest: trustDigest('c')},
	} {
		if _, err := store.Approve(context.Background(), query); err == nil {
			t.Fatalf("invalid query was approved: %#v", query)
		}
	}
}

func mustTrustProjectID(t *testing.T, value string) research.UUID {
	t.Helper()
	id, err := research.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustTrustSourceID(t *testing.T, value string) research.ID {
	t.Helper()
	id, err := research.ParseIDForKind(value, research.KindSource)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func trustDigest(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}
