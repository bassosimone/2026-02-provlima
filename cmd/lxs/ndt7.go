// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
	"github.com/kballard/go-shellquote"
)

func serveNDT7Main(ctx context.Context, args []string) error {
	var (
		formatFlag = "text"
		nameFlag   = "ocho"
	)

	fset := vflag.NewFlagSet("lxs serve ndt7", vflag.ExitOnError)
	fset.StringVar(&formatFlag, 0, "format", "Use `FORMAT` for log output (text or json).")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	mustRun("go build -v ./cmd/gencert")
	mustRun("go build -v ./cmd/ndt7")

	mustRun("./gencert --ip-addr %s", serverAddr)

	mustRun("lxc file push testdata/cert.pem %s-server/root/", nameFlag)
	mustRun("lxc file push testdata/key.pem %s-server/root/", nameFlag)
	mustRun("lxc file push ndt7 %s-server/root/", nameFlag)

	cmdArgv := []string{
		"lxc",
		"exec",
		fmt.Sprintf("%s-server", nameFlag),
		"--",
		"/root/ndt7",
		"serve",
		"-A",
		serverAddr,
		"--format",
		formatFlag,
	}
	mustRun("%s", shellquote.Join(cmdArgv...))

	return nil
}

func measureNDT7Main(ctx context.Context, args []string) error {
	var (
		formatFlag = "text"
		nameFlag   = "ocho"
	)

	fset := vflag.NewFlagSet("lxs measure ndt7", vflag.ExitOnError)
	fset.StringVar(&formatFlag, 0, "format", "Use `FORMAT` for log output (text or json).")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	mustRun("go build -v ./cmd/ndt7")

	mustRun("lxc file push ndt7 %s-client/root/", nameFlag)

	cmdArgv := []string{
		"lxc",
		"exec",
		fmt.Sprintf("%s-client", nameFlag),
		"--",
		"/root/ndt7",
		"measure",
		"-A",
		serverAddr,
		"--format",
		formatFlag,
	}
	mustRun("%s", shellquote.Join(cmdArgv...))

	return nil
}
