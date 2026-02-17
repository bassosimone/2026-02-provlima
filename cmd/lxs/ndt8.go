// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
	"github.com/kballard/go-shellquote"
)

func serveNDT8Main(ctx context.Context, args []string) error {
	var (
		nameFlag = "ocho"
	)

	fset := vflag.NewFlagSet("lxs serve ndt8", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	mustRun("go build -v ./cmd/gencert")
	mustRun("go build -v ./cmd/ndt8")

	mustRun("./gencert --ip-addr %s", serverAddr)

	mustRun("lxc exec %s-server -- mkdir -p /root/static", nameFlag)

	mustRun("lxc file push testdata/cert.pem %s-server/root/", nameFlag)
	mustRun("lxc file push testdata/key.pem %s-server/root/", nameFlag)
	mustRun("lxc file push ndt8 %s-server/root/", nameFlag)
	mustRun("lxc file push static/index.html %s-server/root/static/", nameFlag)
	mustRun("lxc file push static/ndt8.js %s-server/root/static/", nameFlag)

	cmdArgv := []string{
		"lxc",
		"exec",
		fmt.Sprintf("%s-server", nameFlag),
		"--",
		"/root/ndt8",
		"serve",
		"-A",
		serverAddr,
		"--cert",
		"cert.pem",
		"--key",
		"key.pem",
		"-s",
		"static",
	}
	mustRun("%s", shellquote.Join(cmdArgv...))

	return nil
}

func measureNDT8Main(ctx context.Context, args []string) error {
	var (
		http2Flag = false
		nameFlag  = "ocho"
	)

	fset := vflag.NewFlagSet("lxs measure ndt8", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.BoolVar(&http2Flag, '2', "http2", "Force HTTP/2 (default is HTTP/1.1).")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	mustRun("go build -v ./cmd/ndt8")

	mustRun("lxc file push testdata/cert.pem %s-client/root/", nameFlag)
	mustRun("lxc file push ndt8 %s-client/root/", nameFlag)

	cmdArgv := []string{
		"lxc",
		"exec",
		fmt.Sprintf("%s-client", nameFlag),
		"--",
		"/root/ndt8",
		"measure",
		"-A",
		serverAddr,
		"--cert",
		"cert.pem",
	}
	if http2Flag {
		cmdArgv = append(cmdArgv, "-2")
	}
	mustRun("%s", shellquote.Join(cmdArgv...))

	return nil
}
