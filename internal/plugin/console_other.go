//go:build !windows

package plugin

import "os/exec"

// hideConsole does nothing outside Windows, where programs open no window.
func hideConsole(*exec.Cmd) {}
