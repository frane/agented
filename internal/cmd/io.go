package cmd

import (
	"os"
	"path/filepath"
)

// osReadFileImpl wraps os.ReadFile.
func osReadFileImpl(p string) ([]byte, error) {
	return os.ReadFile(p)
}

// writeFileAtomic writes data to path via a temp file + rename to avoid
// readers seeing a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ae-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
