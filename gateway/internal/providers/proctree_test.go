package providers

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func Test別プロセスグループの孫プロセスも終了させる(t *testing.T) {
	// set -m puts the background job in a process group of its own, as
	// agents do with the commands they run.
	cmd := exec.Command("sh", "-c", "set -m; sleep 300 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	child, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatalf("child pid: %q", buf[:n])
	}
	if pg, _ := syscall.Getpgid(child); pg == cmd.Process.Pid {
		t.Fatal("the job shares the shell's process group; the test needs its own")
	}
	procs := Descendants(cmd.Process.Pid)
	found := false
	for _, p := range procs {
		found = found || p == child
	}
	if !found {
		t.Fatalf("descendants %v lack %d", procs, child)
	}
	cmd.Process.Kill()
	cmd.Wait()
	EndProcesses(procs, 2*time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the grandchild is still running")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func Test自分の子孫でないプロセスは列挙しない(t *testing.T) {
	if got := Descendants(0); got != nil {
		t.Errorf("Descendants(0) = %v", got)
	}
}
