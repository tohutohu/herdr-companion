package providers

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Descendants lists the processes below pid (children, grandchildren, …)
// as ps reports them now. Agents start commands in process groups of their
// own, so ending the agent's process does not end them; the list has to be
// taken while they are still attached to the tree.
func Descendants(pid int) []int {
	if pid <= 0 {
		return nil
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		c, err1 := strconv.Atoi(f[0])
		p, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			children[p] = append(children[p], c)
		}
	}
	var all []int
	queue := []int{pid}
	seen := map[int]bool{pid: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !seen[c] {
				seen[c] = true
				all = append(all, c)
				queue = append(queue, c)
			}
		}
	}
	return all
}

// EndProcesses sends SIGTERM to pids, waits up to grace for them to exit
// and kills the rest. Callers pass only processes they started (directly or
// through their agent), listed with Descendants.
func EndProcesses(pids []int, grace time.Duration) {
	if len(pids) == 0 {
		return
	}
	for _, p := range pids {
		syscall.Kill(p, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		alive := false
		for _, p := range pids {
			if syscall.Kill(p, 0) == nil {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, p := range pids {
		syscall.Kill(p, syscall.SIGKILL)
	}
}
