package cmd

import (
	"fmt"

	"github.com/frane/agented/internal/store"
)

// ExtractInput drives ae extract: cut a line range from Path and write it to
// ToFile (created if absent). Atomic via Move's implicit transaction.
type ExtractInput struct {
	Path      string
	FromStart int
	FromEnd   int
	ToFile    string
	ToLine    int  // 0 = append to end of ToFile (default)
	Save      bool // when true, save both files to disk after the move
	Expect    string
}

// ExtractResult summarises the operation.
type ExtractResult struct {
	SrcStateToken string
	DstStateToken string
	SrcSaved      bool
	DstSaved      bool
	LinesMoved    int
}

// Extract is a thin wrapper over Move that:
//   - auto-creates ToFile (Move already does this via prepareWrite),
//   - defaults ToLine to "append" (end of ToFile),
//   - optionally saves both files to disk so refactor flows are one call.
//
// The atomicity guarantee comes from Move's single transaction: any failure
// rolls both edits back; on success the two state tokens are returned.
func (e *Engine) Extract(in ExtractInput) (*Result, error) {
	if in.ToFile == "" {
		return nil, fmt.Errorf("ae extract: --to is required")
	}
	if in.FromStart < 1 || in.FromEnd < in.FromStart {
		return nil, fmt.Errorf("ae extract: invalid --range %d:%d", in.FromStart, in.FromEnd)
	}
	toLine := in.ToLine
	if toLine == 0 {
		// Default: append to end of ToFile. Resolve current line count.
		// Open the file (auto-create if absent) so we can read its line count.
		dstFI, _, _, perr := e.prepareWrite(in.ToFile, true, true)
		if perr != nil {
			return nil, perr
		}
		toLine = dstFI.LineCount
	}
	res, err := e.Move(MoveInput{
		Path:      in.Path,
		FromStart: in.FromStart,
		FromEnd:   in.FromEnd,
		ToFile:    in.ToFile,
		ToLine:    toLine,
		Expect:    in.Expect,
		AutoOpen:  true,
	})
	if err != nil {
		return res, err
	}
	out := &ExtractResult{
		LinesMoved: in.FromEnd - in.FromStart + 1,
	}
	if res.StateToken != "" {
		out.DstStateToken = res.StateToken
	}
	// Source token: re-fetch the source file info.
	if srcFI, ferr := e.Store.FileByPath(in.Path, true); ferr == nil {
		out.SrcStateToken = stateTokenFor(srcFI)
	}
	if in.Save {
		if _, ferr := e.Save(SaveInput{Path: in.Path}); ferr == nil {
			out.SrcSaved = true
		}
		if _, ferr := e.Save(SaveInput{Path: in.ToFile}); ferr == nil {
			out.DstSaved = true
		}
	}
	res.Extract = out
	return res, nil
}

// stateTokenFor returns the state token for a file from its current FileInfo.
func stateTokenFor(fi *store.FileInfo) string {
	if fi == nil {
		return ""
	}
	return store.ComputeStateToken(fi.ID, fi.HeadEditID, fi.ContentHash)
}