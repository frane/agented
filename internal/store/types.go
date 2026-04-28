// Package store is the agented data layer: files, edit tree, marks,
// annotations, transactions, snapshots, and audit log.
//
// Storage model: each edit row stores a forward delta (range, before_text,
// after_text, line_delta) against its parent. Snapshots store full file
// content periodically. Reconstructing content at any edit walks back to
// the nearest snapshot and replays deltas forward.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Sentinel errors. Callers wrap with fmt.Errorf("...: %w", err) where useful.
var (
	ErrFileNotFound       = errors.New("file not registered")
	ErrFileAlreadyOpen    = errors.New("file already open at path")
	ErrEditNotFound       = errors.New("edit not found")
	ErrMarkExists         = errors.New("mark name exists")
	ErrMarkNotFound       = errors.New("mark not found")
	ErrAnnotationNotFound = errors.New("annotation not found")
	ErrBranchAmbiguous    = errors.New("branch ambiguous")
	ErrStateTokenMismatch = errors.New("state_token mismatch")
	ErrTransactionOwned   = errors.New("transaction owned by other actor")
	ErrTransactionState   = errors.New("transaction not in expected state")
	ErrNoTransaction      = errors.New("no open transaction")
	ErrRangeOutOfBounds   = errors.New("range out of bounds")
	ErrCorruptStorage     = errors.New("storage integrity check failed")
)

// FileInfo describes a registered file at a moment in time.
type FileInfo struct {
	ID              int64
	Path            string
	ContentHash     string
	RegisteredAt    time.Time
	ClosedAt        *time.Time
	HeadEditID      int64
	LineCount       int
	AnnotationCount int
}

// IsOpen reports whether the file is currently open (closed_at IS NULL).
func (f *FileInfo) IsOpen() bool { return f.ClosedAt == nil }

// EditInfo describes an edit node in the tree.
type EditInfo struct {
	ID             int64
	FileID         int64
	ParentEditID   *int64
	TransactionID  *int64
	Actor          string
	Command        string
	ArgsJSON       string
	RangeStart     int
	RangeEnd       int
	LineDelta      int
	SnapshotID     *int64
	ContentHash    string
	LineCountAfter int
	CreatedAt      time.Time
	Pruned         bool
}

// Delta describes the delta encoded in an edit row. Used for reconstruction.
// Range semantics: lines [RangeStart, RangeEnd] (1-indexed, inclusive) in the
// parent's content are replaced with AfterText. For pure inserts, RangeEnd =
// RangeStart - 1.
type Delta struct {
	RangeStart int
	RangeEnd   int
	After      []byte
	Before     []byte // optional; not needed for forward replay
	LineDelta  int
}

// Mark is a named line anchor.
type Mark struct {
	ID        int64
	FileID    int64
	Name      string
	EditID    int64
	Line      int
	Snapped   bool
	Actor     string
	CreatedAt time.Time
}

// Annotation is a per-file note.
type Annotation struct {
	ID        int64
	FileID    int64
	Actor     string
	Content   string
	CreatedAt time.Time
	RemovedAt *time.Time
}

// Transaction is an open/committed/rolled_back grouping of edits.
type Transaction struct {
	ID             int64
	Actor          string
	State          string
	StartedAt      time.Time
	EndedAt        *time.Time
	LastActivityAt time.Time
	ScopeFileID    *int64
}

// ComputeStateToken is the deterministic state token for an edit:
//
//	sha256("agented:v1:" + fileID + ":" + headEditID + ":" + contentHashAtHead)
//
// hex-encoded, first 16 chars.
func ComputeStateToken(fileID, headEditID int64, contentHash string) string {
	input := fmt.Sprintf("agented:v1:%d:%d:%s", fileID, headEditID, contentHash)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:16]
}

// HashContent returns the hex SHA-256 of the given content.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// HashBytes returns the hex SHA-256 of the given bytes.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// EpochMs converts a time to unix-epoch-milliseconds (the storage format).
func EpochMs(t time.Time) int64 { return t.UnixMilli() }

// FromEpochMs converts a stored ms timestamp back to a time.Time (UTC).
func FromEpochMs(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// formatID is a tiny helper for error messages.
func formatID(n int64) string { return strconv.FormatInt(n, 10) }
