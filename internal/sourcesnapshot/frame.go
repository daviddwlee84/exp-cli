package sourcesnapshot

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

type frameHash struct{ hash hash.Hash }

func newFrameHash(domain string) *frameHash {
	framed := &frameHash{hash: sha256.New()}
	framed.add("domain", []byte(domain))
	return framed
}

func (framed *frameHash) add(label string, value []byte) {
	writeLength(framed.hash, uint64(len(label)))
	_, _ = framed.hash.Write([]byte(label))
	writeLength(framed.hash, uint64(len(value)))
	_, _ = framed.hash.Write(value)
}

func (framed *frameHash) addString(label, value string) {
	framed.add(label, []byte(value))
}

func (framed *frameHash) addUint(label string, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	framed.add(label, encoded[:])
}

func (framed *frameHash) sum() string {
	return "sha256:" + hex.EncodeToString(framed.hash.Sum(nil))
}

func writeLength(target hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = target.Write(encoded[:])
}
