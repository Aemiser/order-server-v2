//go:build !windows

package wechatlog

import "os/exec"

func hideProviderWindow(_ *exec.Cmd) {}
