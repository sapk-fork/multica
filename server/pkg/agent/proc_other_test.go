//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProcessGroupHasLiveMemberCurrentGroupIsLive verifies that a normal live
// process group is detected as having a live member.
func TestProcessGroupHasLiveMemberCurrentGroupIsLive(t *testing.T) {
	if !processGroupHasLiveMember(syscall.Getpgrp()) {
		t.Fatalf("current process group should have a live member")
	}
}

// TestProcessGroupHasLiveMemberZombieOnlyGroupIsGone verifies that
// processGroupHasLiveMember reports a group as gone once its only remaining
// members are unreaped zombies. This regressed in M-103: signal 0 succeeds for
// zombies, so the old implementation kept waiting until timeout.
func TestProcessGroupHasLiveMemberZombieOnlyGroupIsGone(t *testing.T) {
	leaderPid, childPid, cleanup := startZombieOnlyProcessGroup(t)
	defer cleanup()

	// In container-like runtimes the orphan may be moved out of the group.
	// If it is still present as a zombie in the leader's group, we are
	// exercising the /proc-scan regression fix; otherwise the group is empty
	// and should also be reported as gone.
	if state, pgid, err := readProcStatusStateAndPgid(childPid); err == nil {
		t.Logf("child state=%s pgid=%d (leader pgid=%d)", state, pgid, leaderPid)
	}

	if processGroupHasLiveMember(leaderPid) {
		t.Fatalf("process group %d should have no live members", leaderPid)
	}
}

// TestWaitProcessGroupGoneReturnsWhenGroupIsZombieOnly verifies that
// waitProcessGroupGone returns promptly when a process group has no live
// members, even if unreaped zombies still appear in /proc. This regressed in
// M-103 because signal 0 succeeds for zombies.
func TestWaitProcessGroupGoneReturnsWhenGroupIsZombieOnly(t *testing.T) {
	leaderPid, _, cleanup := startZombieOnlyProcessGroup(t)
	defer cleanup()

	leaderProc, err := os.FindProcess(leaderPid)
	if err != nil {
		t.Fatalf("find process %d: %v", leaderPid, err)
	}

	if !waitProcessGroupGone(leaderProc, 2*time.Second) {
		t.Fatalf("waitProcessGroupGone timed out for group %d", leaderPid)
	}
}

// startZombieOnlyProcessGroup builds and runs a small helper that creates a
// process group whose leader has exited and whose only remaining member is an
// unreaped zombie. It returns the leader's pid (which is also the group id),
// the zombie child's pid, and a cleanup function that reaps the helper.
func startZombieOnlyProcessGroup(t *testing.T) (leaderPid, childPid int, cleanup func()) {
	t.Helper()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "zombie_helper.go")
	exePath := filepath.Join(tempDir, "zombie_helper")
	leaderFile := filepath.Join(tempDir, "leader")
	childFile := filepath.Join(tempDir, "child")
	doneFile := filepath.Join(tempDir, "done")

	if err := os.WriteFile(sourcePath, []byte(zombieOnlyGroupHelperSource), 0o600); err != nil {
		t.Fatalf("write helper source: %v", err)
	}

	build := exec.Command("go", "build", "-o", exePath, sourcePath)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build zombie helper: %v: %s", err, output)
	}

	cmd := exec.Command(exePath, leaderFile, childFile, doneFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zombie helper: %v", err)
	}

	leaderPid, childPid = waitForPidsInFiles(t, leaderFile, childFile)

	// Kill the leader (the helper process itself) so the group becomes
	// leaderless. The zombie child remains in /proc briefly.
	_ = syscall.Kill(leaderPid, syscall.SIGKILL)

	cleanup = func() {
		_ = os.WriteFile(doneFile, []byte{}, 0o600)
		_ = cmd.Wait()
	}
	return
}

// waitForPidsInFiles polls until both pid files contain positive integers.
func waitForPidsInFiles(t *testing.T, leaderFile, childFile string) (leaderPid, childPid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		leaderRaw, lerr := os.ReadFile(leaderFile)
		childRaw, cerr := os.ReadFile(childFile)
		if lerr == nil && cerr == nil {
			leaderPid, _ = strconv.Atoi(strings.TrimSpace(string(leaderRaw)))
			childPid, _ = strconv.Atoi(strings.TrimSpace(string(childRaw)))
			if leaderPid > 0 && childPid > 0 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("helper never wrote pids to %s and %s", leaderFile, childFile)
	return
}

// zombieOnlyGroupHelperSource is a tiny Go program used by
// startZombieOnlyProcessGroup. It puts itself in a new process group, forks a
// child that exits immediately, records both pids, and waits for a done file.
// It never calls Wait on the child, so the child remains an unreaped zombie
// until the helper exits.
const zombieOnlyGroupHelperSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "child" {
		os.Exit(0)
	}
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: zombie_helper <leader-file> <child-file> <done-file>")
		os.Exit(1)
	}
	leaderFile := os.Args[1]
	childFile := os.Args[2]
	doneFile := os.Args[3]

	if err := syscall.Setpgid(0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "setpgid:", err)
		os.Exit(1)
	}

	cmd := exec.Command(os.Args[0], "child")
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start child:", err)
		os.Exit(1)
	}
	childPid := cmd.Process.Pid

	if err := os.WriteFile(leaderFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write leader:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(childFile, []byte(fmt.Sprintf("%d\n", childPid)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write child:", err)
		os.Exit(1)
	}

	for {
		if _, err := os.Stat(doneFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Intentionally do not call cmd.Wait() so the child remains a zombie until
	// this process exits.
}
`
