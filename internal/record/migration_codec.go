package record

import "github.com/daviddwlee84/exp-cli/internal/research"

func decodeCanonicalDocument(data []byte) (*Document, error) {
	document, err := Decode(data)
	if err == nil {
		return document, nil
	}
	return DecodeImported(data)
}

func inventoryUsesImportedProject(inventory *Inventory) bool {
	if inventory == nil || inventory.Project == nil {
		return false
	}
	project, ok := inventory.Project.Record.(*research.Project)
	return ok && project.ProjectID.IsImported()
}

func candidateInventoryForBase(root string, documents []*Document, base *Inventory) *Inventory {
	if inventoryUsesImportedProject(base) {
		return InventoryFromMigratedDocuments(root, documents)
	}
	return InventoryFromDocuments(root, documents)
}

func encodeCandidateForBase(document *Document, base *Inventory) ([]byte, *Document, error) {
	encode := Encode
	decode := Decode
	if inventoryUsesImportedProject(base) {
		encode = EncodeImported
		decode = DecodeImported
	}
	content, err := encode(document)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateRecordSize(content); err != nil {
		return nil, nil, err
	}
	normalized, err := decode(content)
	if err != nil {
		return nil, nil, err
	}
	return content, normalized, nil
}
