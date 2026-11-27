package daemon

import (
	"os"
	"strconv"
	"testing"
)

func TestParentPID(t *testing.T) {
	if got := parentPID(strconv.Itoa(os.Getpid())); got != os.Getppid() {
		t.Fatalf("parentPID = %d, want %d", got, os.Getppid())
	}
	if got := parentPID("0"); got != -1 {
		t.Fatalf("parentPID(0) = %d, want -1", got)
	}
}

func TestStrayXrayIgnoresOthers(t *testing.T) {

	if l := strayXray(os.Getpid()); len(l) != 0 {
		t.Fatalf("strayXray = %v", l)
	}
}
