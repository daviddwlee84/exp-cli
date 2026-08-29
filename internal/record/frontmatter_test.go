package record

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeValidFixtureRecordsAndNormalizeDeterministically(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "v1", "valid-project")
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		switch entry.Name() {
		case "README.md", "ROADMAP.md", "LEDGER.md", "DECISIONS.md":
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 7 {
		t.Fatalf("found %d canonical fixture records, want 7", len(paths))
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			document, err := Decode(data)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			encoded, err := Encode(document)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			again, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode(normalized): %v\n%s", err, encoded)
			}
			reencoded, err := Encode(again)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(encoded, reencoded) {
				t.Fatalf("normalization is not idempotent\nfirst:\n%s\nsecond:\n%s", encoded, reencoded)
			}
			if document.Body != again.Body {
				t.Fatalf("Markdown body changed: %q != %q", document.Body, again.Body)
			}
			if document.Revision != again.Revision {
				t.Fatalf("revision changed across normalization: %s != %s", document.Revision, again.Revision)
			}
		})
	}
}
