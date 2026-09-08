package research

import (
	"crypto/sha256"
	"sync"
)

const safeTextCacheEntries = 4096
const maxCachedTextBytes = 64 << 10

// Validation is a pure function of text and compiled rules. Transaction journal
// verification decodes the same immutable documents many times per invocation;
// cache only successful checks, by digest, without retaining their plaintext.
// Eviction bounds memory and never changes the result of a validation.
var safeTextCache = struct {
	sync.Mutex
	keys map[[32]byte]struct{}
	ring [safeTextCacheEntries][32]byte
	next int
}{keys: make(map[[32]byte]struct{}, safeTextCacheEntries)}

func safeTextCached(value string) ([32]byte, bool) {
	return safeTextCachedDomain(value, 0)
}

func safeTextCachedDomain(value string, domain byte) ([32]byte, bool) {
	if len(value) > maxCachedTextBytes {
		return [32]byte{}, false
	}
	input := make([]byte, len(value)+1)
	input[0] = domain
	copy(input[1:], value)
	key := sha256.Sum256(input)
	safeTextCache.Lock()
	_, found := safeTextCache.keys[key]
	safeTextCache.Unlock()
	return key, found
}

func rememberSafeText(value string, key [32]byte) {
	if len(value) > maxCachedTextBytes {
		return
	}
	safeTextCache.Lock()
	defer safeTextCache.Unlock()
	if _, found := safeTextCache.keys[key]; found {
		return
	}
	if len(safeTextCache.keys) == safeTextCacheEntries {
		delete(safeTextCache.keys, safeTextCache.ring[safeTextCache.next])
	}
	safeTextCache.keys[key] = struct{}{}
	safeTextCache.ring[safeTextCache.next] = key
	safeTextCache.next = (safeTextCache.next + 1) % safeTextCacheEntries
}
