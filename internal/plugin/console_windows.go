package plugin

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW, which starts a console program without
// a console of its own.
const createNoWindow = 0x08000000

// hideConsole keeps a console program from opening a console window, which
// it would, started from an app with none.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
