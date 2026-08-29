package projection

import (
	"context"
	"errors"
	"testing"
)

func TestReadProjectionContentHonorsCancellationBetweenBoundedReads(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancelAfterProjectionRead{cancel: cancel}
	expectedBytes := projectionReadChunkSize * 3

	content, err := readProjectionContent(ctx, reader, expectedBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readProjectionContent() = %d bytes, %v; want cancellation", len(content), err)
	}
	if reader.reads != 1 {
		t.Fatalf("reader calls after cancellation = %d, want 1", reader.reads)
	}
}

type cancelAfterProjectionRead struct {
	cancel context.CancelFunc
	reads  int
}

func (reader *cancelAfterProjectionRead) Read(buffer []byte) (int, error) {
	reader.reads++
	for index := range buffer {
		buffer[index] = 'x'
	}
	reader.cancel()
	return len(buffer), nil
}
