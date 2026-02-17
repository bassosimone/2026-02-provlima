// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
)

// policy describes a network emulation policy.
type policy struct {
	delay      string
	download   string
	upload     string
	tbfLatency string
}

// policies maps named profiles to their [policy] definitions.
//
// These profiles are loosely inspired by Chrome DevTools' network
// throttling presets (see "Network conditions" in Chrome DevTools,
// or chromium/src/third_party/devtools-frontend/src/front_end/
// core/sdk/NetworkManager.ts for the source definitions). We use
// more realistic values reflecting typical real-world conditions:
//
//   - 2g: EDGE connection (600ms RTT, 200/50 kbps), representing
//     practical EDGE throughput in areas with only 2G coverage.
//   - 3g: HSPA connection (200ms RTT, 3/1 Mbps), representing
//     basic 3G practical throughput below HSPA+ speeds.
//   - 4g: typical urban LTE (100ms RTT, 30/10 Mbps), per 3GPP
//     Release 10 Cat 6 practical throughput ranges.
//   - 5g: typical sub-6 GHz 5G NR (20ms RTT, 100/30 Mbps),
//     representing practical mid-band 5G throughput.
//   - poor-mobile: degraded mobile or rural connection (150ms RTT,
//     5/1 Mbps), similar to Chrome DevTools' "Slow 4G" but with
//     higher throughput to represent partial coverage scenarios.
//   - broadband: typical home connection (50ms RTT, 100/20 Mbps),
//     comparable to VDSL2 or entry-level cable.
//   - ftth-100: fiber-to-the-home at 100 Mbps (10ms RTT, 100/50
//     Mbps), reflecting typical GPON provisioning with low latency.
//   - ftth-1g: fiber-to-the-home at 1 Gbps (10ms RTT, 1000/500
//     Mbps), representing high-end GPON residential service.
//   - server: data center or server-to-server link (2ms RTT,
//     delay only). Real DC links run at 10–100 Gbps, which is
//     beyond what tc can meaningfully shape on a veth pair, so
//     this profile only adds delay without rate limiting.
//
// The tbfLatency field controls the maximum time a packet may sit in
// the TBF queue before being dropped. Low values (e.g., 50ms) model
// well-managed networks; high values (e.g., 500ms–2s) simulate
// bufferbloat — the condition where oversized router/modem buffers
// cause latency to spike under load, which is exactly what the
// "responsiveness" metric is designed to detect.
var policies = map[string]policy{
	"2g":                  {"300ms", "200kbit", "50kbit", "50ms"},
	"2g-bloated":          {"300ms", "200kbit", "50kbit", "1000ms"},
	"3g":                  {"100ms", "3mbit", "1mbit", "50ms"},
	"3g-bloated":          {"100ms", "3mbit", "1mbit", "500ms"},
	"4g":                  {"50ms", "30mbit", "10mbit", "50ms"},
	"4g-bloated":          {"50ms", "30mbit", "10mbit", "500ms"},
	"5g":                  {"10ms", "100mbit", "30mbit", "50ms"},
	"5g-bloated":          {"10ms", "100mbit", "30mbit", "500ms"},
	"poor-mobile":         {"75ms", "5mbit", "1mbit", "50ms"},
	"poor-mobile-bloated": {"75ms", "5mbit", "1mbit", "500ms"},
	"broadband":           {"25ms", "100mbit", "20mbit", "50ms"},
	"broadband-bloated":   {"25ms", "100mbit", "20mbit", "1000ms"},
	"ftth-100":            {"5ms", "100mbit", "50mbit", "50ms"},
	"ftth-100-bloated":    {"5ms", "100mbit", "50mbit", "500ms"},
	"ftth-1g":             {"5ms", "1gbit", "500mbit", "50ms"},
	"ftth-1g-bloated":     {"5ms", "1gbit", "500mbit", "500ms"},
	"server":              {"1ms", "", "", ""},
}

// rateToBPS converts a tc rate string (e.g., "100mbit") to bits per second.
func rateToBPS(rate string) (int, error) {
	rate = strings.TrimSpace(rate)
	for _, suffix := range []struct {
		s string
		m int
	}{
		{"gbit", 1_000_000_000},
		{"mbit", 1_000_000},
		{"kbit", 1_000},
	} {
		if numStr, ok := strings.CutSuffix(rate, suffix.s); ok {
			num, err := strconv.Atoi(numStr)
			if err != nil {
				return 0, fmt.Errorf("invalid rate %q: %w", rate, err)
			}
			return num * suffix.m, nil
		}
	}
	num, err := strconv.Atoi(rate)
	if err != nil {
		return 0, fmt.Errorf("invalid rate %q: %w", rate, err)
	}
	return num, nil
}

// computeBurst returns a TBF burst size in bytes scaled to the given rate.
//
// The Token Bucket Filter (TBF, see tc-tbf(8)) requires a "burst"
// parameter: the maximum number of bytes that can be sent
// instantaneously before rate limiting kicks in. The bucket must
// be large enough to accommodate at least one MTU-sized packet,
// otherwise the kernel may silently drop traffic.
//
// We size the burst to 10ms worth of data at the given rate
// (rate_bps / 100 / 8), which gives the shaper enough runway to
// absorb small traffic spikes without materially affecting the
// sustained rate. A floor of 32 KiB (32768 bytes) ensures the
// bucket stays well above typical MTU sizes (~1500 bytes) even
// at very low rates.
func computeBurst(rate string) int {
	bps := runtimex.LogFatalOnError1(rateToBPS(rate))
	burst := max(bps/100/8, 32768)
	return burst
}

// applyNetem applies network emulation rules on the router container.
//
// It clears existing rules first, then installs qdiscs on the router's
// eth1 (toward client) and eth2 (toward server). When the policy includes
// rate limits (non-empty download/upload), it creates a two-layer chain:
//
//  1. netem (root): adds the configured one-way delay.
//  2. tbf (child): enforces the rate limit with token bucket filtering.
//
// When download and upload are empty (e.g., the "server" profile),
// only the netem delay qdisc is installed — no rate shaping is
// applied. This is used for links where the real bandwidth exceeds
// what tc can meaningfully shape on a veth pair (e.g., 10–100 Gbps
// data center links).
//
// The TBF "latency" parameter (policy.tbfLatency) caps the maximum
// time a packet may wait in the TBF queue before being dropped.
// This controls the queue depth and therefore the degree of
// bufferbloat: low values (50ms) model well-managed networks where
// queuing delay stays bounded; high values (500ms–2s) simulate the
// oversized buffers found in many real-world routers and modems,
// causing latency to spike under load.
//
// Although the containers run on the same host, LXC gives each
// container a veth pair with a standard 1500-byte MTU on eth0,
// so the traffic shaping behaves realistically — packets are
// segmented and queued as they would be on a real network link.
func applyNetem(name string, p policy) {
	clearNetem(name)

	rateShaping := p.download != "" && p.upload != ""

	// Router eth1 (toward client): delay + optional download rate shaping
	if rateShaping {
		dlBurst := computeBurst(p.download)
		fmt.Fprintf(os.Stderr, "router eth1 (toward client): %s delay, %s rate, %dB burst, %s tbf-latency\n",
			p.delay, p.download, dlBurst, p.tbfLatency)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth1 root handle 1: netem delay %s",
			name, p.delay)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth1 parent 1:1 handle 10: tbf rate %s burst %d latency %s",
			name, p.download, dlBurst, p.tbfLatency)
	} else {
		fmt.Fprintf(os.Stderr, "router eth1 (toward client): %s delay, no rate shaping\n", p.delay)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth1 root handle 1: netem delay %s",
			name, p.delay)
	}

	// Router eth2 (toward server): delay + optional upload rate shaping
	if rateShaping {
		ulBurst := computeBurst(p.upload)
		fmt.Fprintf(os.Stderr, "router eth2 (toward server): %s delay, %s rate, %dB burst, %s tbf-latency\n",
			p.delay, p.upload, ulBurst, p.tbfLatency)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth2 root handle 1: netem delay %s",
			name, p.delay)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth2 parent 1:1 handle 10: tbf rate %s burst %d latency %s",
			name, p.upload, ulBurst, p.tbfLatency)
	} else {
		fmt.Fprintf(os.Stderr, "router eth2 (toward server): %s delay, no rate shaping\n", p.delay)
		mustRun("lxc exec %s-router -- tc qdisc add dev eth2 root handle 1: netem delay %s",
			name, p.delay)
	}

	fmt.Fprintf(os.Stderr, "\neffective RTT: 2 x %s\n", p.delay)
	if rateShaping {
		fmt.Fprintf(os.Stderr, "download: %s, upload: %s\n", p.download, p.upload)
		fmt.Fprintf(os.Stderr, "tbf-latency: %s (bufferbloat simulation)\n", p.tbfLatency)
	} else {
		fmt.Fprintf(os.Stderr, "rate shaping: none (unlimited)\n")
	}
}

// clearNetem removes all tc qdisc rules from the router, ignoring errors.
func clearNetem(name string) {
	fmt.Fprintf(os.Stderr, "clearing: %s-router eth1 and eth2\n", name)
	// Note: commands may fail if no previous policy had been set
	run("lxc exec %s-router -- tc qdisc del dev eth1 root", name)
	run("lxc exec %s-router -- tc qdisc del dev eth2 root", name)
}

// netemApplyMain is the main of the `lxs netem apply` command.
func netemApplyMain(ctx context.Context, args []string) error {
	var (
		nameFlag       = "ocho"
		templateFlag   = ""
		delayFlag      = ""
		downloadFlag   = ""
		uploadFlag     = ""
		tbfLatencyFlag = ""
	)

	fset := vflag.NewFlagSet("lxs netem apply", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	fset.StringVar(&templateFlag, 't', "template", "Load named `TEMPLATE` as a starting point (overridable by other flags). "+
		"Available: 2g, 3g, 4g, 5g, poor-mobile, broadband, ftth-100, ftth-1g, server "+
		"(all except server also have a -bloated variant).")
	fset.StringVar(&delayFlag, 0, "delay", "One-way `DELAY` (e.g., 25ms).")
	fset.StringVar(&downloadFlag, 0, "download", "Download `RATE` (e.g., 100mbit).")
	fset.StringVar(&uploadFlag, 0, "upload", "Upload `RATE` (e.g., 20mbit).")
	fset.StringVar(&tbfLatencyFlag, 0, "tbf-latency", "TBF queue `LATENCY` for bufferbloat simulation (e.g., 50ms, 1000ms).")
	runtimex.PanicOnError0(fset.Parse(args))

	var p policy
	if templateFlag != "" {
		var ok bool
		p, ok = policies[templateFlag]
		if !ok {
			log.Fatalf("unknown template: %s", templateFlag)
		}
	}

	// Let explicit flags override the (possibly template-loaded) policy.
	if delayFlag != "" {
		p.delay = delayFlag
	}
	if downloadFlag != "" {
		p.download = downloadFlag
	}
	if uploadFlag != "" {
		p.upload = uploadFlag
	}
	if tbfLatencyFlag != "" {
		p.tbfLatency = tbfLatencyFlag
	}

	// Require at least something to be configured.
	if p.delay == "" {
		log.Fatal("specify --template or at least --delay")
	}

	// Apply default tbfLatency if still empty.
	if p.tbfLatency == "" {
		p.tbfLatency = "50ms"
	}

	applyNetem(nameFlag, p)
	return nil
}

// netemClearMain is the main of the `lxs netem clear` command.
func netemClearMain(ctx context.Context, args []string) error {
	var (
		nameFlag = "ocho"
	)

	fset := vflag.NewFlagSet("lxs netem clear", vflag.ExitOnError)
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&nameFlag, 'n', "name", "Use `NAME` to name LXC resources.")
	runtimex.PanicOnError0(fset.Parse(args))

	clearNetem(nameFlag)
	return nil
}
