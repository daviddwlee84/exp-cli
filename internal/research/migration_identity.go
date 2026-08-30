package research

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	HarnessV0Namespace = "b2e8b68c-2de6-5291-885e-19f0efdfe218"
	HarnessV0Reader    = "exp.harness-v0-reader/v1"
)

var harnessV0NamespaceUUID = uuid.MustParse(HarnessV0Namespace)

// ImportedProjectID derives the deterministic UUIDv5 project identity from an
// exact lower-case SHA-256 tree fingerprint.
func ImportedProjectID(fingerprint string) (UUID, error) {
	fingerprint = strings.TrimPrefix(fingerprint, "sha256:")
	if !validLowerHex(fingerprint, 64) {
		return UUID{}, fmt.Errorf("invalid harness-v0 tree fingerprint")
	}
	value := uuid.NewSHA1(harnessV0NamespaceUUID, []byte("tree-sha256:"+fingerprint))
	return NewImportedProjectUUID(value)
}

// ImportedRecordID derives one typed UUIDv5 from its imported Project and
// stable source key.
func ImportedRecordID(project UUID, kind Kind, stableSourceKey string) (ID, error) {
	if !project.IsImported() {
		return ID{}, fmt.Errorf("imported record requires a UUIDv5 project identity")
	}
	if err := validateIDKind(kind); err != nil {
		return ID{}, err
	}
	if stableSourceKey == "" || strings.ContainsRune(stableSourceKey, '\x00') {
		return ID{}, fmt.Errorf("stable source key is empty or contains NUL")
	}
	value := uuid.NewSHA1(project.UUID, []byte(kind.String()+"\x00"+stableSourceKey))
	return NewImportedID(kind, value)
}

func validLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
