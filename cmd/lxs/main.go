// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/bassosimone/vclip"
	"github.com/bassosimone/vflag"
)

func main() {
	serveDisp := vclip.NewDispatcherCommand("lxs serve", vflag.ExitOnError)
	serveDisp.AddCommand("ndt7", vclip.CommandFunc(serveNDT7Main), "Run ndt7 service")
	serveDisp.AddCommand("ndt8", vclip.CommandFunc(serveNDT8Main), "Run ndt8 service")

	measureDisp := vclip.NewDispatcherCommand("lxs measure", vflag.ExitOnError)
	measureDisp.AddCommand("ndt7", vclip.CommandFunc(measureNDT7Main), "Measure with ndt7")
	measureDisp.AddCommand("ndt8", vclip.CommandFunc(measureNDT8Main), "Measure with ndt8")

	netemDisp := vclip.NewDispatcherCommand("lxs netem", vflag.ExitOnError)
	netemDisp.AddCommand("apply", vclip.CommandFunc(netemApplyMain), "Apply network emulation.")
	netemDisp.AddCommand("clear", vclip.CommandFunc(netemClearMain), "Clear network emulation.")

	disp := vclip.NewDispatcherCommand("lxs", vflag.ExitOnError)

	disp.AddCommand("create", vclip.CommandFunc(createMain), "Create containers.")
	disp.AddCommand("destroy", vclip.CommandFunc(destroyMain), "Destroy containers.")
	disp.AddCommand("iperf", vclip.CommandFunc(iperfMain), "Run iperf3.")
	disp.AddCommand("measure", measureDisp, "Run measurements.")
	disp.AddCommand("netem", netemDisp, "Manage network emulation.")
	disp.AddCommand("serve", serveDisp, "Run servers.")

	vclip.Main(context.Background(), disp, os.Args[1:])
}
