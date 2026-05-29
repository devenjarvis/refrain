package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// testBin is the path to the refrain binary built once for the entire test run.
var testBin string

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	dir, err := os.MkdirTemp("", "refrain-cmd-test-*")
	if err != nil {
		panic("creating temp dir: " + err.Error())
	}
	defer func() { _ = os.RemoveAll(dir) }()

	bin := filepath.Join(dir, "refrain")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		panic("building refrain: " + err.Error() + "\n" + string(out))
	}
	testBin = bin

	os.Exit(m.Run())
}
