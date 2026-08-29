package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/daviddwlee84/exp-cli/internal/pathx"
	"github.com/daviddwlee84/exp-cli/internal/record"
	"github.com/daviddwlee84/exp-cli/internal/research"
)

const (
	projectReceiptFile   = "project-receipt.json"
	projectReceiptSchema = "exp.project-init-receipt/v1"
	maxProjectReceipt    = 1 << 20
)

type projectReceipt struct {
	Schema  string `json:"schema"`
	Content []byte `json:"project_content"`
	Hash    string `json:"project_hash"`
}

type receiptState struct {
	document *record.Document
	content  []byte
	raw      []byte
	info     fs.FileInfo
}

func readProjectReceipt(root *os.Root) (*receiptState, error) {
	if _, err := root.Lstat(projectReceiptFile); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect project initialization receipt: %w", err)
	}
	file, openedInfo, err := pathx.OpenRegularFileNoFollow(root, projectReceiptFile)
	if err != nil {
		return nil, fmt.Errorf("open project initialization receipt: %w", err)
	}
	defer file.Close()
	state := &receiptState{info: openedInfo}
	if err := validateReceiptFile(file); err != nil {
		return state, err
	}
	if err := file.Chmod(0o600); err != nil {
		return state, fmt.Errorf("protect project initialization receipt: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxProjectReceipt+1))
	if err != nil {
		return state, fmt.Errorf("read project initialization receipt: %w", err)
	}
	if len(raw) > maxProjectReceipt {
		return state, errors.New("project initialization receipt exceeds size limit")
	}
	state.raw = append([]byte(nil), raw...)
	finalInfo, err := root.Lstat(projectReceiptFile)
	if err != nil || finalInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, finalInfo) {
		return state, fmt.Errorf("project initialization receipt changed while reading: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var receipt projectReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return state, fmt.Errorf("decode project initialization receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return state, errors.New("project initialization receipt has trailing JSON content")
	}
	if receipt.Schema != projectReceiptSchema {
		return state, fmt.Errorf("unsupported project initialization receipt schema %q", receipt.Schema)
	}
	if receipt.Hash != contentHash(receipt.Content) {
		return state, errors.New("project initialization receipt content hash does not match")
	}
	document, err := record.Decode(receipt.Content)
	if err != nil {
		return state, fmt.Errorf("decode Project from initialization receipt: %w", err)
	}
	if document.Kind() != research.KindProject {
		return state, errors.New("project initialization receipt does not contain a Project record")
	}
	document.Path = record.ProjectFile
	state.document = document
	state.content = append([]byte(nil), receipt.Content...)
	return state, nil
}

func writeProjectReceipt(root *os.Root, content []byte, current *receiptState, hook record.AtomicHook, verify ...func() error) error {
	receipt := projectReceipt{Schema: projectReceiptSchema, Content: append([]byte(nil), content...), Hash: contentHash(content)}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode project initialization receipt: %w", err)
	}
	raw = append(raw, '\n')
	options := record.AtomicWriteOptions{Mode: 0o600, Hook: hook}
	if len(verify) > 0 {
		options.Verify = func() error {
			var result error
			for _, check := range verify {
				if check != nil {
					result = errors.Join(result, check())
				}
			}
			return result
		}
	}
	if current != nil {
		if bytes.Equal(current.raw, raw) {
			return nil
		}
		options.Expected = current.info
		options.ExpectedContent = current.raw
	}
	writeErr := record.AtomicWriteDerivedRoot(root, projectReceiptFile, raw, options)
	if writeErr != nil {
		return fmt.Errorf("publish project initialization receipt: %w", writeErr)
	}
	return nil
}

func verifyProjectReceipt(root *os.Root, expected *receiptState) error {
	if expected == nil || expected.info == nil || expected.raw == nil {
		return errors.New("project initialization receipt has no verifiable snapshot")
	}
	content, info, err := pathx.ReadBoundedRegularFile(context.Background(), root, projectReceiptFile, maxProjectReceipt)
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected.info) {
		return errors.New("project initialization receipt changed identity")
	}
	if !bytes.Equal(content, expected.raw) {
		return errors.New("project initialization receipt changed content")
	}
	return nil
}

func sameProjectIdentity(left, right *record.Document) bool {
	if left == nil || right == nil {
		return false
	}
	leftProject, leftOK := left.Record.(*research.Project)
	rightProject, rightOK := right.Record.(*research.Project)
	return leftOK && rightOK && leftProject.ProjectID == rightProject.ProjectID && leftProject.CreatedAt.Equal(rightProject.CreatedAt)
}

func projectIdentity(document *record.Document) string {
	if document == nil {
		return "<nil>"
	}
	project, ok := document.Record.(*research.Project)
	if !ok {
		return "<non-project>"
	}
	return project.ProjectID.String() + "@" + project.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
