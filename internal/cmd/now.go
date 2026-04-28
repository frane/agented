package cmd

import "time"

// now returns the current time in UTC; declared as a small indirection so
// tests can override.
var now = func() time.Time { return time.Now().UTC() }
