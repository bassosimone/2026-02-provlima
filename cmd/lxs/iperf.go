// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
	"github.com/kballard/go-shellquote"
)

func iperfMain(ctx context.Context, args []string) error {
	var (
		congestionFlag = ""
		nameFlag       = "ocho"
		reverseFlag    = false
		udpFlag        = false
	)

	fset := vflag.NewFlagSet("lxs iperf", vflag.ExitOnError)
	fset.StringVar(&congestionFlag, 'C', "congestion", "Set congestion control algorithm.")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	fset.BoolVar(&reverseFlag, 'R', "reverse", "Run an upload test.")
	fset.BoolVar(&udpFlag, 'u', "udp", "Use UDP instead of TCP.")
	fset.DisablePermute = true
	runtimex.PanicOnError0(fset.Parse(args))

	iperfArgv := []string{"lxc", "exec", fmt.Sprintf("%s-client", nameFlag), "--", "iperf3", "-c", serverAddr}
	if congestionFlag != "" {
		iperfArgv = append(iperfArgv, "-C", congestionFlag)
	}
	if reverseFlag {
		iperfArgv = append(iperfArgv, "-R")
	}
	if udpFlag {
		iperfArgv = append(iperfArgv, "-u")
	}

	mustRun("%s", shellquote.Join(iperfArgv...))
	return nil
}
