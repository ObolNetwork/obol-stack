//go:build unix

package main

import "syscall"

// detachedSysProcAttr returns the platform-specific SysProcAttr that
// detaches a spawned child from the parent's process group. On Unix this
// is a new session leader (Setsid: true) so the child survives the
// parent's exit and does not receive SIGHUP / SIGINT propagated from
// the controlling terminal.
//
// Linux/macOS/BSD share the same shape via the unix build tag. If we
// ever ship a Windows obol binary, add sell_proc_windows.go with a
// CREATE_NEW_PROCESS_GROUP equivalent.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
