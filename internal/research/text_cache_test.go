package research

import (
	"errors"
	"fmt"
	"github.com/daviddwlee84/exp-cli/internal/safex"
	"strings"
	"sync"
	"testing"
)

func TestTextCachePreservesValidationAcrossEvictionAndConcurrentReads(t *testing.T) {
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 600; i++ {
				text := fmt.Sprintf("Observation %d for run %d", i, worker)
				for _, value := range []string{text, "password=cache-test-credential", text + "\x00"} {
					cached, uncached := ValidateCommitSafeText(value), validateCommitSafeTextUncached(value)
					if (cached == nil) != (uncached == nil) || errors.Is(cached, ErrUnsafeText) != errors.Is(uncached, ErrUnsafeText) {
						t.Errorf("cached validation differs from compiled rules")
					}
				}
			}
		}(worker)
	}
	wait.Wait()
	large := strings.Repeat("x", maxCachedTextBytes+1)
	if ValidateCommitSafeText(large) != nil || validateCommitSafeTextUncached(large) != nil {
		t.Fatal("uncached large text changed behavior")
	}
	safeTextCache.Lock()
	defer safeTextCache.Unlock()
	if len(safeTextCache.keys) > safeTextCacheEntries {
		t.Fatal("validation cache exceeded its bound")
	}
}

func TestTextCacheKeepsPercentDecodedCredentialRulesSeparate(t *testing.T) {
	for _, text := range []string{"ordinary description", "password%3Dprivate-encoded-value", "%70%61%73%73%77%6f%72%64%3dprivate-value", "unsafe\x00control"} {
		for i := 0; i < 3; i++ {
			_ = ValidateCommitSafeText(text)
			if got, want := containsCredentialMaterial(text), safex.ContainsSecretText(safex.DecodePercentEncoding(text)); got != want {
				t.Fatal("text cache mixed distinct validation rules")
			}
			if (ValidateCommitSafeText(text) == nil) != (validateCommitSafeTextUncached(text) == nil) {
				t.Fatal("credential cache changed ordinary text validation")
			}
		}
	}
}

func BenchmarkRepeatedCommitSafeText(b *testing.B) {
	text := "sha256:" + strings.Repeat("a", 64)
	for b.Loop() {
		if err := ValidateCommitSafeText(text); err != nil {
			b.Fatal(err)
		}
	}
}
