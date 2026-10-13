package cli

import (
	"testing"
)

func TestNewRootCmd_ReturnsDistinctInstances(t *testing.T) {
	cmd1 := NewRootCmd()
	cmd2 := NewRootCmd()

	if cmd1 == cmd2 {
		t.Error("NewRootCmd() called multiple times returns the same command pointer")
	}
}
