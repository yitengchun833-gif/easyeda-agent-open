package daemon

import (
	"os/exec"
	"syscall"
)

func hideKnowledgeWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
