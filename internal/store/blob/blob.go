// Package blob provides a small Codec interface for storing edit blobs
// (before_text / after_text / snapshot content) in the SQLite database.
//
// The default codec is gzip with two adaptive escape hatches:
//   - if the plaintext is smaller than rawThreshold bytes, store raw with
//     a raw tag (compression is overhead);
//   - if the gzip-encoded form is larger than the plaintext (already-
//     compressed or random data), store raw with the raw tag.
//
// All encoded blobs carry a 1-byte tag prefix:
//
//	0x00  raw bytes follow
//	0x01  gzip-compressed bytes follow
//
// Decode dispatches on the tag.
package blob

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

const (
	tagRaw  = byte(0x00)
	tagGzip = byte(0x01)

	// rawThreshold: payloads smaller than this never use compression. Single-
	// line edits dominate by count and are usually well under this size.
	rawThreshold = 64
)

// Codec is the encode/decode interface used by the store. Implementations
// must be safe for concurrent use.
type Codec interface {
	Encode(plain []byte) ([]byte, error)
	Decode(blob []byte) ([]byte, error)
	Name() string
}

// Default returns the codec the store uses unless overridden in tests.
func Default() Codec { return gzipCodec{} }

// gzipCodec is the default encoding: gzip with raw fallback.
type gzipCodec struct{}

// Name returns the codec name (used by debug/diagnostic logs).
func (gzipCodec) Name() string { return "gzip+raw" }

// Encode wraps `plain` with a 1-byte tag and either gzip output or raw bytes.
// Empty input encodes to a single tag byte plus no payload (still a valid raw
// blob — Decode returns nil bytes).
func (gzipCodec) Encode(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return []byte{tagRaw}, nil
	}
	if len(plain) < rawThreshold {
		out := make([]byte, 0, 1+len(plain))
		out = append(out, tagRaw)
		out = append(out, plain...)
		return out, nil
	}
	var buf bytes.Buffer
	buf.WriteByte(tagGzip)
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(plain); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	if buf.Len()-1 >= len(plain) {
		// Compression made it larger; fall back to raw.
		out := make([]byte, 0, 1+len(plain))
		out = append(out, tagRaw)
		out = append(out, plain...)
		return out, nil
	}
	return buf.Bytes(), nil
}

// Decode reverses Encode. Empty (nil) input returns empty bytes; this
// supports the "no blob recorded" case.
func (gzipCodec) Decode(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	switch b[0] {
	case tagRaw:
		out := make([]byte, len(b)-1)
		copy(out, b[1:])
		return out, nil
	case tagGzip:
		gr, err := gzip.NewReader(bytes.NewReader(b[1:]))
		if err != nil {
			return nil, fmt.Errorf("blob: gzip header: %w", err)
		}
		defer gr.Close()
		out, err := io.ReadAll(gr)
		if err != nil {
			return nil, fmt.Errorf("blob: gzip body: %w", err)
		}
		return out, nil
	default:
		return nil, errors.New("blob: unknown codec tag")
	}
}
