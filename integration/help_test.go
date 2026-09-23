package integration

import (
	"os/exec"
	"regexp"
	"testing"

	"github.com/naggie/dstask"
)

// Every command dstask dispatches must be on the help page. Tools read the
// command list from it, so a missing entry hides a command that works.
func TestHelpListsEveryCommand(t *testing.T) {
	out, err := exec.Command(binaryPath(), "help").CombinedOutput()
	if err != nil && len(out) == 0 {
		t.Fatalf("dstask help produced no output: %v", err)
	}

	for _, cmd := range dstask.ALL_CMDS {
		if cmd == dstask.CMD_COMPLETIONS {
			continue // internal, used by the shell completion scripts
		}
		listed := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(cmd) + `\s+:`)
		if !listed.Match(out) {
			t.Errorf("help page does not list %q", cmd)
		}
	}
}
