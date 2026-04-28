package store_test

import (
	"fmt"
	"testing"

	"github.com/frane/agented/internal/store"
)

// BenchmarkReconstruct10000Edits builds a chain of 10,000 edits then measures
// the time to reconstruct content at the head. Reconstruction must traverse
// at most snapshot_interval (=64) deltas because of the snapshot policy.
func BenchmarkReconstruct10000Edits(b *testing.B) {
	s, dir := newStoreBench(b)
	p := writeFileBench(b, dir, "f.txt", "1\n2\n3\n")
	o, err := s.OpenFile("p", p)
	if err != nil {
		b.Fatal(err)
	}
	tok := o.StateToken
	for i := 0; i < 10000; i++ {
		r, _, err := s.Replace(o.File.ID, 1, 1, fmt.Sprintf("X%d\n", i),
			store.EditOptions{Actor: "p", ExpectStateToken: tok}, "writes")
		if err != nil {
			b.Fatal(err)
		}
		tok = r.NewStateToken
	}
	// Drop cache so we measure cold reconstruction.
	s.SetCacheSize(0)
	s.SetCacheSize(16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.HeadContent(o.File.ID); err != nil {
			b.Fatal(err)
		}
	}
}
