// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
)

const (
	clientAddr = "192.168.0.2"
	serverAddr = "192.168.1.2"
)

func createMain(ctx context.Context, args []string) error {
	var (
		nameFlag = "ocho"
	)

	fset := vflag.NewFlagSet("lxs create", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	mustRun("lxc network create %s-left ipv4.address=none ipv6.address=none", nameFlag)
	mustRun("lxc network create %s-right ipv4.address=none ipv6.address=none", nameFlag)

	mustRun("lxc launch images:debian/bookworm %s-client", nameFlag)
	mustRun("lxc launch images:debian/bookworm %s-router", nameFlag)
	mustRun("lxc launch images:debian/bookworm %s-server", nameFlag)

	mustRun("lxc network attach %s-left %s-client eth1", nameFlag, nameFlag)
	mustRun("lxc network attach %s-left %s-router eth1", nameFlag, nameFlag)
	mustRun("lxc network attach %s-right %s-router eth2", nameFlag, nameFlag)
	mustRun("lxc network attach %s-right %s-server eth1", nameFlag, nameFlag)

	mustRun("lxc exec %s-client -- ip addr add %s/24 dev eth1", nameFlag, clientAddr)
	mustRun("lxc exec %s-client -- ip link set eth1 up", nameFlag)
	mustRun("lxc exec %s-client -- ip route add 192.168.1.0/24 via 192.168.0.1", nameFlag)

	mustRun("lxc exec %s-router -- ip addr add 192.168.0.1/24 dev eth1", nameFlag)
	mustRun("lxc exec %s-router -- ip link set eth1 up", nameFlag)
	mustRun("lxc exec %s-router -- ip addr add 192.168.1.1/24 dev eth2", nameFlag)
	mustRun("lxc exec %s-router -- ip link set eth2 up", nameFlag)
	mustRun("lxc exec %s-router -- sysctl net.ipv4.ip_forward=1", nameFlag)

	mustRun("lxc exec %s-server -- ip addr add %s/24 dev eth1", nameFlag, serverAddr)
	mustRun("lxc exec %s-server -- ip link set eth1 up", nameFlag)
	mustRun("lxc exec %s-server -- ip route add 192.168.0.0/24 via 192.168.1.1", nameFlag)

	mustRun("lxc exec %s-client -- apt update", nameFlag)
	mustRun("lxc exec %s-client --env DEBIAN_FRONTEND=noninteractive -- apt install -y iperf3", nameFlag)

	mustRun("lxc exec %s-server -- apt update", nameFlag)
	mustRun("lxc exec %s-server --env DEBIAN_FRONTEND=noninteractive -- apt install -y iperf3", nameFlag)
	mustRun("lxc exec %s-server -- systemctl enable iperf3", nameFlag)
	mustRun("lxc exec %s-server -- service iperf3 start", nameFlag)

	return nil
}
