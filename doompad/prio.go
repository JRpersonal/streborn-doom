package main

// While the game runs, Doom and this tool get the CPU first and STR's own
// background processes step back (they have nothing to do during a game).
// Every change is undone on exit. Bose's audio server keeps its own priority:
// the sound depends on it.

import (
	"fmt"
	"syscall"
)

type prioChange struct {
	pid, was int
}

func getNice(pid int) (int, error) {
	// getpriority returns 20 - nice on Linux
	r, _, e := syscall.Syscall(syscall.SYS_GETPRIORITY, 0, uintptr(pid), 0)
	if e != 0 {
		return 0, e
	}
	return 20 - int(r), nil
}

// renice sets nice for pid and remembers the old value.
func renice(pid, nice int, undo *[]prioChange) {
	if pid == 0 {
		return
	}
	was, err := getNice(pid)
	if err != nil {
		return
	}
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, nice); err != nil {
		fmt.Println("prio:", pid, err)
		return
	}
	*undo = append(*undo, prioChange{pid, was})
}

func restorePrio(undo []prioChange) {
	for _, c := range undo {
		syscall.Setpriority(syscall.PRIO_PROCESS, c.pid, c.was)
	}
}
