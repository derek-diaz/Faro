package redundancy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var gzipHeader = []byte{0x1f, 0x8b}

// encodeSnapshot preserves the original JSON wire format while it fits within
// the transport limit. Larger snapshots are compressed; hosts files compress
// particularly well, and decodeSnapshot remains compatible with older raw
// snapshots already stored by Faro.
func encodeSnapshot(snapshot ConfigSnapshot, transportLimit int) ([]byte, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotUncompressedBytes {
		return nil, errors.New("generated DNS configuration is too large to synchronize")
	}
	if len(raw) <= transportLimit {
		return raw, nil
	}

	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() > transportLimit {
		return nil, fmt.Errorf(
			"generated DNS configuration is too large to synchronize even after compression (%d MiB; limit %d MiB)",
			compressed.Len()/(1<<20), transportLimit/(1<<20),
		)
	}
	return compressed.Bytes(), nil
}

func decodeSnapshot(payload []byte) (ConfigSnapshot, error) {
	reader := io.Reader(bytes.NewReader(payload))
	if bytes.HasPrefix(payload, gzipHeader) {
		compressed, err := gzip.NewReader(reader)
		if err != nil {
			return ConfigSnapshot{}, errors.New("controller configuration snapshot is invalid")
		}
		defer compressed.Close()
		reader = compressed
	} else if len(payload) > maxSnapshotUncompressedBytes {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot is too large")
	}

	raw, err := io.ReadAll(io.LimitReader(reader, maxSnapshotUncompressedBytes+1))
	if err != nil {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot is invalid")
	}
	if len(raw) > maxSnapshotUncompressedBytes {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot is too large")
	}
	var snapshot ConfigSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return ConfigSnapshot{}, errors.New("controller configuration snapshot is invalid")
	}
	return snapshot, nil
}
