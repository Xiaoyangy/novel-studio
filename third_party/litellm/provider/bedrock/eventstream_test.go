package bedrock

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/voocel/litellm/internal/testgolden"
)

func TestReadEventStreamMessageReadsPayload(t *testing.T) {
	reader := bufio.NewReader(bytes.NewReader(testgolden.ReadFixture(t, "../../testdata/bedrock/eventstream.bin")))
	payload, err := readEventStreamMessage(reader)
	if err != nil {
		t.Fatalf("readEventStreamMessage: %v", err)
	}
	if string(payload) != `{"contentBlockDelta":{"contentBlockIndex":0,"delta":{"text":"hel"}}}` {
		t.Fatalf("payload = %s", payload)
	}
}

func TestReadEventStreamMessageRejectsInvalidLength(t *testing.T) {
	var frame [12]byte
	binary.BigEndian.PutUint32(frame[0:4], 15)
	_, err := readEventStreamMessage(bufio.NewReader(bytes.NewReader(frame[:])))
	if err == nil || !strings.Contains(err.Error(), "invalid message length") {
		t.Fatalf("expected invalid message length, got %v", err)
	}
}

func TestReadEventStreamMessageRejectsInvalidHeadersLength(t *testing.T) {
	var frame [16]byte
	binary.BigEndian.PutUint32(frame[0:4], 16)
	binary.BigEndian.PutUint32(frame[4:8], 1)
	_, err := readEventStreamMessage(bufio.NewReader(bytes.NewReader(frame[:])))
	if err == nil || !strings.Contains(err.Error(), "invalid headers length") {
		t.Fatalf("expected invalid headers length, got %v", err)
	}
}
