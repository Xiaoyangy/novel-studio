package bedrock

import (
	"bufio"
	"fmt"
	"io"
)

func (s *stream) readEventStreamMessage() ([]byte, error) {
	return readEventStreamMessage(s.reader)
}

func readEventStreamMessage(reader *bufio.Reader) ([]byte, error) {
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(reader, prelude); err != nil {
		return nil, err
	}
	totalLength := readBigEndianUint32(prelude[0:4])
	headersLength := readBigEndianUint32(prelude[4:8])
	if totalLength < 16 || totalLength > 16*1024*1024 {
		return nil, fmt.Errorf("invalid message length: %d", totalLength)
	}
	if headersLength > totalLength-16 {
		return nil, fmt.Errorf("invalid headers length: %d > %d", headersLength, totalLength-16)
	}
	remaining := make([]byte, totalLength-12)
	if _, err := io.ReadFull(reader, remaining); err != nil {
		return nil, err
	}
	payloadLength := totalLength - headersLength - 16
	if payloadLength == 0 {
		return nil, nil
	}
	payloadStart := int(headersLength)
	payloadEnd := payloadStart + int(payloadLength)
	if payloadEnd > len(remaining)-4 || payloadStart > payloadEnd {
		return nil, fmt.Errorf("invalid payload bounds: start=%d, end=%d, remaining=%d", payloadStart, payloadEnd, len(remaining))
	}
	return remaining[payloadStart:payloadEnd], nil
}

func readBigEndianUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
