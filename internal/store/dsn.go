package store

import (
	"net/url"
)

const (
	pragmaKey = "_pragma"
	txlockKey = "_txlock"
	txLockArg = "immediate"
)

var pragmaArgs = []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)"}

// buildDSN expects a validated, non-empty path
func buildDSN(dbPath string) string {
	v := make(url.Values)

	for _, a := range pragmaArgs {
		v.Add(pragmaKey, a)
	}
	v.Add(txlockKey, txLockArg)

	return dbPath + "?" + v.Encode()
}
