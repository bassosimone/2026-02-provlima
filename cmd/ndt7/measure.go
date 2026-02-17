// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/bassosimone/2026-02-provlima/internal/slogging"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
)

func measureMain(ctx context.Context, args []string) error {
	var (
		addressFlag = "127.0.0.1"
		formatFlag  = "text"
		portFlag    = "4567"
	)

	fset := vflag.NewFlagSet("ndt7 measure", vflag.ExitOnError)
	fset.StringVar(&addressFlag, 'A', "address", "Use the given IP `ADDRESS`.")
	fset.StringVar(&formatFlag, 0, "format", "Use `FORMAT` for log output (text or json).")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&portFlag, 'p', "port", "Use the given TCP `PORT`.")
	runtimex.PanicOnError0(fset.Parse(args))

	slogging.Setup(formatFlag)

	host := net.JoinHostPort(addressFlag, portFlag)

	dlURL := fmt.Sprintf("wss://%s/ndt/v7/download", host)
	slog.Info("download", slog.String("url", dlURL))
	conn, err := dial(ctx, dlURL, true)
	runtimex.LogFatalOnError0(err)
	receiver(ctx, conn, "download")

	ulURL := fmt.Sprintf("wss://%s/ndt/v7/upload", host)
	slog.Info("upload", slog.String("url", ulURL))
	conn, err = dial(ctx, ulURL, true)
	runtimex.LogFatalOnError0(err)
	sender(ctx, conn, "upload")

	return nil
}
