package cmd

import (
	"github.com/frane/agented/internal/db"
	"github.com/frane/agented/internal/store"
)

// PruneInput is the input to prune.
type PruneInput struct {
	ClosedOlderThan      string // duration string; "" = use config
	DeadBranches         bool
	DeadBranchesIdleFor  string
	KeepRecentPerBranch  int
	OrphanMarks          bool
	Vacuum               bool
	DryRun               bool
	Confirm              bool
	FileID               *int64
}

// Prune runs a manual prune.
func (e *Engine) Prune(in PruneInput) (*Result, error) {
	opts := store.PruneOptions{
		FileID: in.FileID,
		DryRun: in.DryRun,
	}
	if in.ClosedOlderThan != "" {
		d, err := parseDuration(in.ClosedOlderThan)
		if err != nil {
			return nil, err
		}
		opts.ClosedFilesOlderThan = d
	} else {
		opts.ClosedFilesOlderThan = e.Config.ClosedFilesOlderThan()
	}
	if in.DeadBranches {
		if in.DeadBranchesIdleFor != "" {
			d, err := parseDuration(in.DeadBranchesIdleFor)
			if err != nil {
				return nil, err
			}
			opts.DeadBranchesIdleFor = d
		} else {
			opts.DeadBranchesIdleFor = e.Config.DeadBranchesIdleFor()
		}
	} else {
		// Disable dead-branch pruning when not requested.
		opts.DeadBranchesIdleFor = -1
	}
	if in.KeepRecentPerBranch > 0 {
		opts.KeepRecentPerBranch = in.KeepRecentPerBranch
	}
	opts.OrphanMarks = in.OrphanMarks
	rep, err := e.Store.Prune(e.Actor, opts)
	if err != nil {
		return nil, err
	}
	if in.Vacuum && !in.DryRun {
		if err := e.Store.Vacuum(); err != nil {
			return nil, err
		}
	}
	return &Result{Prune: &PruneResult{Report: *rep}}, nil
}

// PruneAuditInput is the input to prune-audit.
type PruneAuditInput struct {
	OlderThan string
	DryRun    bool
	Confirm   bool
}

// PruneAudit removes old audit log entries.
func (e *Engine) PruneAudit(in PruneAuditInput) (*Result, error) {
	d, err := parseDuration(in.OlderThan)
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		// Count without deleting.
		var n int64
		_ = e.Store.DB().QueryRow(
			`SELECT COUNT(*) FROM audit_log WHERE created_at < ?`,
			now().UnixMilli()-d.Milliseconds(),
		).Scan(&n)
		return &Result{Prune: &PruneResult{Report: store.PruneReport{
			DryRun: true,
			Details: []string{formatPlural(n, "audit entry", "audit entries") + " would be removed"},
		}}}, nil
	}
	n, err := e.Store.AuditPrune(d)
	if err != nil {
		return nil, err
	}
	return &Result{Prune: &PruneResult{Report: store.PruneReport{
		Details: []string{formatPlural(n, "audit entry", "audit entries") + " removed"},
	}}}, nil
}

// Who returns the current actor.
func (e *Engine) Who() *Result {
	return &Result{Who: &WhoResult{Actor: e.Actor}}
}

// VersionInput is empty.
type VersionInput struct {
	Version string
	Commit  string
	Date    string
}

// Version returns binary metadata.
func (e *Engine) Version(in VersionInput) *Result {
	return &Result{Version: &VersionResult{
		Version: in.Version, Commit: in.Commit, Date: in.Date,
		SchemaVersion: db.CurrentSchemaVersion,
	}}
}

func formatPlural(n int64, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return formatInt(n) + " " + plural
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
