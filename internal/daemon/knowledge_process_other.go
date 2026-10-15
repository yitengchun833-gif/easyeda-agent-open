//go:build !windows

package daemon

import "os/exec"

func hideKnowledgeWindow(cmd *exec.Cmd) {}
