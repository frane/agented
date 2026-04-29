package cmd

import "os"

// osReadFileImpl wraps os.ReadFile so callers stay decoupled from os in
// their signatures. Production writes go through internal/atomicfile.
func osReadFileImpl(p string) ([]byte, error) {
	return os.ReadFile(p)
}
