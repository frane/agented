package cmd

import (
	"time"

	"github.com/frane/agented/internal/config"
)

// parseDuration is a thin wrapper around config.ParseDuration so verb code
// doesn't import config directly for this one helper.
func parseDuration(s string) (time.Duration, error) {
	return config.ParseDuration(s)
}
