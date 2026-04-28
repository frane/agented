package blob_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/frane/agented/internal/store/blob"
)

func TestRoundtripEmpty(t *testing.T) {
	c := blob.Default()
	enc, err := c.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec) != 0 {
		t.Errorf("expected empty decode, got %d bytes", len(dec))
	}
}

func TestRoundtripTiny(t *testing.T) {
	c := blob.Default()
	for _, in := range [][]byte{[]byte("x"), []byte("hello"), []byte("a\nb\n")} {
		enc, err := c.Encode(in)
		if err != nil {
			t.Fatal(err)
		}
		// Tiny payloads must use raw tag (encoded[0] == 0x00).
		if enc[0] != 0x00 {
			t.Errorf("tiny payload not raw-tagged: tag=%x", enc[0])
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dec, in) {
			t.Errorf("mismatch: %q != %q", dec, in)
		}
	}
}

func TestRoundtripLargeCompressible(t *testing.T) {
	c := blob.Default()
	in := []byte(strings.Repeat("hello world\n", 200))
	enc, err := c.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	if enc[0] != 0x01 {
		t.Errorf("compressible payload not gzip-tagged: tag=%x len=%d", enc[0], len(enc))
	}
	if len(enc) >= len(in) {
		t.Errorf("expected compression to shrink: %d -> %d", len(in), len(enc))
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, in) {
		t.Errorf("mismatch")
	}
}

func TestRoundtripLargeIncompressible(t *testing.T) {
	c := blob.Default()
	in := make([]byte, 1024)
	if _, err := rand.Read(in); err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	// Random bytes don't compress; expect raw fallback.
	if enc[0] != 0x00 {
		t.Errorf("incompressible payload not raw-tagged: tag=%x", enc[0])
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, in) {
		t.Errorf("mismatch")
	}
}

func TestRoundtripVariousSizes(t *testing.T) {
	c := blob.Default()
	for _, n := range []int{0, 1, 7, 63, 64, 100, 1024, 4096, 64 * 1024, 1 * 1024 * 1024} {
		in := make([]byte, n)
		if n > 0 {
			rand.Read(in)
		}
		enc, err := c.Encode(in)
		if err != nil {
			t.Fatalf("encode %d: %v", n, err)
		}
		dec, err := c.Decode(enc)
		if err != nil {
			t.Fatalf("decode %d: %v", n, err)
		}
		if !bytes.Equal(dec, in) {
			t.Errorf("roundtrip mismatch at n=%d", n)
		}
	}
}

func TestUnknownTagErrors(t *testing.T) {
	c := blob.Default()
	if _, err := c.Decode([]byte{0xFF, 1, 2, 3}); err == nil {
		t.Error("expected error for unknown tag")
	}
}
