//go:build unix

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// cancelReapSemanticsReliable caches the result of probing whether this
// runtime keeps orphaned children in their parent's process group after the
// leader exits. Some container runtimes (e.g. rootless Podman-based agent
// sandboxes) reparent orphaned children to init and move them to a different
// process group. In that environment group-targeted SIGKILL cannot reach the
// descendants, so cancellation tests that assert whole-process-group reaping
// cannot be verified.
var cancelReapSemanticsReliable struct {
	once sync.Once
	ok   bool
}

// skipIfOrphanReapMovesOutOfGroup skips t when this runtime moves orphaned
// children out of their original process group. The probe is cached so the
// cost is paid only once per package test run.
func skipIfOrphanReapMovesOutOfGroup(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	cancelReapSemanticsReliable.once.Do(func() {
		cancelReapSemanticsReliable.ok = processGroupReapSemanticsReliable(t)
	})
	if !cancelReapSemanticsReliable.ok {
		t.Skip("runtime moves orphaned children out of the original process group; group-targeted SIGKILL cannot be verified here")
	}
}

// processGroupReapSemanticsReliable probes the runtime by starting a process
// group leader that respects SIGTERM and a descendant that ignores SIGTERM.
// After SIGTERM-ing the group, the leader should be reaped and the descendant
// should remain in the same process group. If the descendant is still alive
// but no longer in the leader's process group, the runtime's reap semantics
// are unreliable for group-targeted termination.
func processGroupReapSemanticsReliable(t *testing.T) bool {
	tempDir := t.TempDir()
	pidFile := filepath.Join(tempDir, "pids")
	scriptPath := filepath.Join(tempDir, "probe")

	script := "#!/bin/sh\n" +
		"trap 'exit 0' TERM\n" +
		"( trap '' TERM; sleep 300 ) </dev/null >/dev/null 2>&1 &\n" +
		"child=$!\n" +
		"printf '%s %s %s\\n' \"$$\" \"$child\" \"$(awk '{print $5}' /proc/self/stat)\" > " + pidFile + "\n" +
		"sleep 300\n"
	writeTestExecutable(t, scriptPath, []byte(script))

	cmd := exec.Command(scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start probe: %v", err)
	}

	var leader, child int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				leader, _ = strconv.Atoi(fields[0])
				child, _ = strconv.Atoi(fields[1])
				if leader > 0 && child > 0 {
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leader == 0 || child == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("probe never recorded pids")
	}

	// Signal the whole group with SIGTERM. The leader respects it; the
	// descendant ignores it. In normal Linux semantics the descendant stays in
	// the original process group.
	_ = syscall.Kill(-leader, syscall.SIGTERM)
	time.Sleep(300 * time.Millisecond)

	state, pgid, err := readTestProcStatusStateAndPgid(child)
	reliable := true
	if err == nil && state != "Z" && pgid != leader {
		// Child is alive and has been moved out of the leader's group.
		reliable = false
	}

	// Best-effort cleanup of any survivors.
	_ = syscall.Kill(-leader, syscall.SIGKILL)
	_ = syscall.Kill(leader, syscall.SIGKILL)
	_ = syscall.Kill(child, syscall.SIGKILL)
	_ = cmd.Wait()
	return reliable
}

// readTestProcStatusStateAndPgid returns the State and Pgid fields from
// /proc/<pid>/status. It is only used by the process-group reap probe.
func readTestProcStatusStateAndPgid(pid int) (state string, pgid int, err error) {
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
