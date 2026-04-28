// Package actor resolves the active actor identity for a CLI invocation.
//
// Resolution order (highest precedence first):
//  1. --as flag (passed as `flag` argument).
//  2. AE_ACTOR environment variable.
//  3. Config file (resolved by caller, passed as `cfgActor`).
//  4. Fallback: hostname:pid.
//
// The actor must never be empty. If determination somehow yields an empty
// string, Resolve returns an error.
package actor

import (
	"errors"
	"fmt"
	"os"
)

// ErrEmpty is returned if actor resolution yields an empty identity.
var ErrEmpty = errors.New("actor identity could not be determined")

// Resolve returns the actor for this invocation. flag and cfgActor may be
// empty; envName defaults to "AE_ACTOR".
func Resolve(flag, cfgActor string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if v := os.Getenv("AE_ACTOR"); v != "" {
		return v, nil
	}
	if cfgActor != "" {
		return cfgActor, nil
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "host"
	}
	pid := os.Getpid()
	out := fmt.Sprintf("%s:%d", host, pid)
	if out == "" {
		return "", ErrEmpty
	}
	return out, nil
}
