package pmon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type testListener struct {
	t        *testing.T
	listener *ProcListener
	done     chan bool
	acks     []EventAck
	forks    []EventFork
	execs    []EventExec
	uids     []EventUid
	gids     []EventGid
	sids     []EventSid
	exits    []EventExit
}

func newTestListener(t *testing.T) *testListener {
	tl := &testListener{
		t:        t,
		listener: &ProcListener{},
		done:     make(chan bool, 1),
	}

	err := tl.listener.Connect()
	if err != nil {
		t.Fatal("Failed connect")
	}

	go tl.listener.ListenEvents()
	go func() {
		for {
			select {
			case <-tl.done:
				return
			case <-tl.listener.Error:
				t.Fatal("Error on recv")
			case event := <-tl.listener.EventAck:
				fmt.Printf("%T no=%d\n", event, event.No)
				tl.acks = append(tl.acks, *event)
			case event := <-tl.listener.EventFork:
				fmt.Printf("%T ppid=%d ptid=%d cpid=%d ctid=%d\n",
					event, event.ParentPid, event.ParentTid, event.ChildPid, event.ChildTid)
				tl.forks = append(tl.forks, *event)
			case event := <-tl.listener.EventExec:
				fmt.Printf("%T pid=%d tid=%d\n", event, event.Pid, event.Tid)
				tl.execs = append(tl.execs, *event)
			case event := <-tl.listener.EventUid:
				fmt.Printf("%T pid=%d tid=%d ruid=%d euid=%d\n",
					event, event.Pid, event.Tid, event.Ruid, event.Euid)
				tl.uids = append(tl.uids, *event)
			case event := <-tl.listener.EventGid:
				fmt.Printf("%T pid=%d tid=%d ruid=%d euid=%d\n",
					event, event.Pid, event.Tid, event.Rgid, event.Egid)
				tl.gids = append(tl.gids, *event)
			case event := <-tl.listener.EventSid:
				fmt.Printf("%T pid=%d tid=%d\n",
					event, event.Pid, event.Tid)
				tl.sids = append(tl.sids, *event)
			case event := <-tl.listener.EventExit:
				fmt.Printf("%T pid=%d tid=%d code=%d signal=%d\n",
					event, event.Pid, event.Tid, event.Code, event.Signal)
				tl.exits = append(tl.exits, *event)
			}
		}
	}()

	return tl
}

func (tl *testListener) close() {
	pause := 100 * time.Millisecond
	time.Sleep(pause)
	tl.done <- true
	tl.listener.Close()
	time.Sleep(pause)
}

func TestAck(t *testing.T) {
	tl := newTestListener(t)
	tl.close()

	if len(tl.acks) != 1 && tl.acks[0].No != 0 {
		t.Errorf("Expected 1 ack event")
	}
}

func TestForkAndUidAndGid(t *testing.T) {
	parentPid := os.Getpid()
	tl := newTestListener(t)

	childPid, _, err := syscall.Syscall(syscall.SYS_FORK, 0, 0, 0)
	if err != 0 {
		t.Fatal("Error on fork syscall")
	}

	childGid := 65534
	childUid := 1000

	if childPid == 0 {
		_, _, err := syscall.Syscall(syscall.SYS_SETSID, 0, 0, 0)
		if err != 0 {
			fmt.Println("SYS_SETSID error:", err)
			os.Exit(1)
		}

		_, _, err = syscall.Syscall(syscall.SYS_SETREGID, uintptr(childGid), uintptr(childGid), 0)
		if err != 0 {
			fmt.Println("SYS_SETREGID error:", err)
			os.Exit(1)
		}

		_, _, err = syscall.Syscall(syscall.SYS_SETREUID, uintptr(childUid), uintptr(childUid), 0)
		if err != 0 {
			fmt.Println("SYS_SETREUID error:", err)
			os.Exit(1)
		}

		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}

	tl.close()
	if len(tl.forks) < 1 {
		t.Errorf("Expected at least 1 fork event")
	}

	forkFound := false
	for _, event := range tl.forks {
		if event.ParentPid == uint32(parentPid) && event.ChildPid == uint32(childPid) {
			forkFound = true
		}
	}

	if !forkFound {
		t.Errorf("Not found expected fork event")
	}

	gidFound := false
	for _, event := range tl.gids {
		if event.Rgid == uint32(childGid) && event.Egid == uint32(childGid) {
			gidFound = true
		}
	}

	if !gidFound {
		t.Errorf("Not found expected gid event")
	}

	uidFound := false
	for _, event := range tl.uids {
		if event.Ruid == uint32(childUid) && event.Euid == uint32(childUid) {
			uidFound = true
		}
	}

	if !uidFound {
		t.Errorf("Not found expected uid event")
	}

	sidFound := false
	for _, event := range tl.sids {
		if event.Pid == uint32(childPid) {
			sidFound = true
		}
	}

	if !sidFound {
		t.Errorf("Not found expected sid event")
	}
}

func TestExecAndExitSuccess(t *testing.T) {
	tl := newTestListener(t)
	cmd := exec.Command("sleep", "0.1")
	if err := cmd.Run(); err != nil {
		t.Fatal("Error on exec command:", err)
	}

	pid := uint32(cmd.Process.Pid)
	tl.close()

	if len(tl.execs) < 1 {
		t.Errorf("Expected at least 1 exec event")
	}

	execFound := false
	for _, event := range tl.execs {
		if event.Pid == pid {
			execFound = true
		}
	}

	if !execFound {
		t.Errorf("Not found expected fork event")
	}

	for _, event := range tl.exits {
		if event.Pid == pid && event.Code == 0 {
			return
		}
	}

	t.Errorf("Not found expected exit event")
}

func TestExecAndExitBySignal(t *testing.T) {
	tl := newTestListener(t)

	cmd := exec.Command("sleep", "100")
	if err := cmd.Start(); err != nil {
		t.Fatal("Error on exec command:", err)
	}

	pid := uint32(cmd.Process.Pid)
	sig := syscall.SIGTERM

	syscall.Kill(cmd.Process.Pid, sig)
	cmd.Wait()

	tl.close()

	if len(tl.execs) < 1 {
		t.Errorf("Expected at least 1 exec event")
	}

	execFound := false
	for _, event := range tl.execs {
		if event.Pid == pid {
			execFound = true
		}
	}

	if !execFound {
		t.Errorf("Not found expected fork event")
	}

	for _, event := range tl.exits {
		if event.Pid == pid && event.Code == uint32(sig) {
			return
		}
	}

	t.Errorf("Not found expected exit event")
}
