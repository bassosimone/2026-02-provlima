// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
)

func destroyMain(ctx context.Context, args []string) error {
	var (
		nameFlag = "ocho"
	)

	fset := vflag.NewFlagSet("lxs destroy", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	run("lxc stop %s-client", nameFlag)
	run("lxc delete %s-client", nameFlag)
	run("lxc stop %s-router", nameFlag)
	run("lxc delete %s-router", nameFlag)
	run("lxc stop %s-server", nameFlag)
	run("lxc delete %s-server", nameFlag)

	run("lxc network delete %s-left", nameFlag)
	run("lxc network delete %s-right", nameFlag)

	return nil
}
