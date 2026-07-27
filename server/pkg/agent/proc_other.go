//go:build !windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// hideAgentWindow is a no-op on non-Windows platforms.
func hideAgentWindow(cmd *exec.Cmd) {}

// configureProcessGroup puts the child into its own process group (it becomes
// the group leader, so the group id equals the child pid). This lets the
// daemon signal the entire tree — the agent CLI plus any tool subprocess it
// spawns — in one call, instead of killing only the direct child and leaking
// grandchildren that keep running (and, for opencode, spinning on EPIPE) after
// a task is cancelled or the daemon restarts. See signalProcessGroup.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func codexInitializeRetrySupported() bool { return true }

// signalProcessGroup sends sig to the whole process group led by p (when the
// command was started with configureProcessGroup), falling back to the single
// process if the group send fails. Targeting the group (negative pid) reaches
// the descendants the agent spawned, not just the leader.
func signalProcessGroup(p *os.Process, sig syscall.Signal) {
	if p == nil {
		return
	}
	if err := syscall.Kill(-p.Pid, sig); err != nil {
		_ = p.Signal(sig)
	}
}

func waitProcessGroupGone(p *os.Process, timeout time.Duration) bool {
	if p == nil {
		return false
	}
	pgid := p.Pid
	deadline := time.Now().Add(timeout)
	for {
		if !processGroupHasLiveMember(pgid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// processGroupHasLiveMember reports whether any process in pgid is still
// running (i.e. not a zombie waiting to be reaped). Signal 0 succeeds for
// zombies that have not yet been reaped, so in container-like runtimes where
// orphaned children linger in the process table for a while we also scan /proc
// and treat a group whose only remaining members are zombies as gone.
func processGroupHasLiveMember(pgid int) bool {
	if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		// Cannot inspect /proc; fall back to the conservative signal check.
		return true
	}
	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		state, procPgid, err := readProcStatusStateAndPgid(pid)
		if err != nil || procPgid != pgid {
			continue
		}
		if state != "Z" {
			return true
		}
	}
	return false
}

// readProcStatusStateAndPgid returns the State and Pgid fields from
// /proc/<pid>/status. It is a small, best-effort parser used by
// processGroupHasLiveMember; if the entry disappears mid-read it returns an
// error so the caller can ignore that pid.
func readProcStatusStateAndPgid(pid int) (state string, pgid int, err error) {
	data, rerr := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if rerr != nil {
		return "", 0, rerr
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				state = fields[1]
			}
		} else if strings.HasPrefix(line, "Pgid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				pgid, _ = strconv.Atoi(fields[1])
			}
		}
	}
	if state == "" {
		return "", 0, fmt.Errorf("missing state for pid %d", pid)
	}
	return state, pgid, nil
}
