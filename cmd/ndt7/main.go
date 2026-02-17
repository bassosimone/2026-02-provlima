// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	disp := vclip.NewDispatcherCommand("lxs", vflag.ExitOnError)

	disp.AddCommand("measure", vclip.CommandFunc(measureMain), "Measure performance.")
	disp.AddCommand("serve", vclip.CommandFunc(serveMain), "Serve requests.")

	vclip.Main(context.Background(), disp, os.Args[1:])
}
