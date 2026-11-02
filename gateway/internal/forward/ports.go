package forward

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Port is a TCP port something listens on that localhost reaches.
type Port struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Command string `json:"command"`
	// Cwd is the listening process's working directory, when it could be read;
	// the app puts servers started in a session's folder first.
	Cwd string `json:"cwd,omitempty"`
}

// ListPorts reads the listening TCP ports from lsof, leaving out the Gateway's own.
func ListPorts(ctx context.Context) ([]Port, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	if err != nil && len(out) == 0 {
		// lsof exits 1 when nothing listens.
		if _, ok := err.(*exec.ExitError); ok {
			return []Port{}, nil
		}
		return nil, err
	}
	ports := parseListening(string(out), os.Getpid())
	if len(ports) == 0 {
		return ports, nil
	}
	pids := make([]string, 0, len(ports))
	seen := map[int]bool{}
	for _, p := range ports {
		if !seen[p.PID] {
			seen[p.PID] = true
			pids = append(pids, strconv.Itoa(p.PID))
		}
	}
	if cwdOut, err := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-p", strings.Join(pids, ","), "-Fpn").Output(); err == nil || len(cwdOut) > 0 {
		cwds := parseCwds(string(cwdOut))
		for i := range ports {
			ports[i].Cwd = cwds[ports[i].PID]
		}
	}
	return ports, nil
}

// parseListening reads `lsof -Fpcn` output: one port per number, only for
// addresses localhost reaches (loopback or every interface).
func parseListening(out string, skipPID int) []Port {
	byPort := map[int]Port{}
	var pid int
	var command string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(v)
			command = ""
		case 'c':
			command = v
		case 'n':
			if pid == skipPID {
				continue
			}
			host, portText, err := net.SplitHostPort(v)
			if err != nil {
				continue
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				continue
			}
			if host != "*" && host != "localhost" {
				ip := net.ParseIP(host)
				if ip == nil || !(ip.IsLoopback() || ip.IsUnspecified()) {
					continue
				}
			}
			if _, ok := byPort[port]; !ok {
				byPort[port] = Port{Port: port, PID: pid, Command: command}
			}
		}
	}
	ports := make([]Port, 0, len(byPort))
	for _, p := range byPort {
		ports = append(ports, p)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports
}

// parseCwds reads `lsof -a -d cwd -Fpn` output into pid → directory.
func parseCwds(out string) map[int]string {
	cwds := map[int]string{}
	var pid int
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			cwds[pid] = line[1:]
		}
	}
	return cwds
}
