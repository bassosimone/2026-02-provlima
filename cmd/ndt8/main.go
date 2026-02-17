// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	disp := vclip.NewDispatcherCommand("ndt8", vflag.ExitOnError)

	disp.AddCommand("measure", vclip.CommandFunc(measureMain), "Run a measurement.")
	disp.AddCommand("serve", vclip.CommandFunc(serveMain), "Serve requests.")

	vclip.Main(context.Background(), disp, os.Args[1:])
}
