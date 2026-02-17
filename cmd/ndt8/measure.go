// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/bassosimone/2026-02-provlima/internal/infinite"
	"github.com/bassosimone/2026-02-provlima/internal/slogging"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// initialChunkSize is the starting chunk size for doubling (32 bytes).
const initialChunkSize = 32

// maxChunkSize is the maximum chunk size (256 MiB).
const maxChunkSize = 256 << 20

// timeBudget is the total time budget per direction.
const timeBudget = 10 * time.Second

func measureMain(ctx context.Context, args []string) error {
	var (
		addressFlag = "127.0.0.1"
		certFlag    = "testdata/cert.pem"
		http2Flag   = false
		portFlag    = "4443"
	)

	fset := vflag.NewFlagSet("ndt8 measure", vflag.ExitOnError)
	fset.StringVar(&addressFlag, 'A', "address", "Use the given IP `ADDRESS`.")
	fset.StringVar(&certFlag, 0, "cert", "Use `FILE` as the CA certificate.")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.BoolVar(&http2Flag, '2', "http2", "Force HTTP/2 (default is HTTP/1.1).")
	fset.StringVar(&portFlag, 'p', "port", "Use the given TCP `PORT`.")
	runtimex.PanicOnError0(fset.Parse(args))

	// Load the CA certificate to trust the server's self-signed cert.
	caCert := runtimex.LogFatalOnError1(os.ReadFile(certFlag))
	caPool := x509.NewCertPool()
	runtimex.Assert(caPool.AppendCertsFromPEM(caCert))

	tlsConfig := &tls.Config{
		RootCAs: caPool,
	}
	if !http2Flag {
		// Disable HTTP/2 by setting NextProtos to only http/1.1.
		tlsConfig.NextProtos = []string{"http/1.1"}
	}

	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: http2Flag,
	}
	client := &http.Client{Transport: transport}

	baseURL := &url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(addressFlag, portFlag),
	}

	// 1. Create session.
	sid := createSession(ctx, client, baseURL)
	slog.Info("session created", slog.String("sid", sid))

	// 2. Run download with concurrent probes.
	slog.Info("starting download")
	runWithProbes(ctx, client, baseURL, sid, "download")

	// 3. Run upload with concurrent probes.
	slog.Info("starting upload")
	runWithProbes(ctx, client, baseURL, sid, "upload")

	// 4. Delete session.
	deleteSession(ctx, client, baseURL, sid)

	slog.Info("measurement complete", slog.String("sid", sid))
	return nil
}

func createSession(ctx context.Context, client *http.Client, baseURL *url.URL) string {
	u := baseURL.JoinPath("/ndt/v8/session")
	req := runtimex.LogFatalOnError1(http.NewRequestWithContext(ctx, "POST", u.String(), http.NoBody))
	resp := runtimex.LogFatalOnError1(client.Do(req))
	defer resp.Body.Close()

	runtimex.Assert(resp.StatusCode == http.StatusCreated)
	var result struct {
		SessionID string `json:"sessionID"`
	}
	runtimex.LogFatalOnError0(json.NewDecoder(resp.Body).Decode(&result))
	return result.SessionID
}

func deleteSession(ctx context.Context, client *http.Client, baseURL *url.URL, sid string) {
	u := baseURL.JoinPath(fmt.Sprintf("/ndt/v8/session/%s", sid))
	req, err := http.NewRequestWithContext(ctx, "DELETE", u.String(), http.NoBody)
	if err != nil {
		slog.Warn("delete session request failed", slog.Any("err", err))
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("delete session failed", slog.Any("err", err))
		return
	}
	resp.Body.Close()
	slog.Info("session deleted", slog.String("sid", sid), slog.Int("status", resp.StatusCode))
}

// runWithProbes runs chunk-doubling transfers with concurrent probes.
func runWithProbes(ctx context.Context, client *http.Client, baseURL *url.URL, sid, direction string) {
	ctx, cancel := context.WithTimeout(ctx, timeBudget)
	defer cancel()

	// Start probes in background.
	var wg sync.WaitGroup
	wg.Go(func() {
		runProbes(ctx, client, baseURL, sid)
	})

	// Run chunk-doubling transfers.
	for size := int64(initialChunkSize); size <= maxChunkSize; size *= 2 {
		if ctx.Err() != nil {
			break
		}
		switch direction {
		case "download":
			doDownload(ctx, client, baseURL, sid, size)
		case "upload":
			doUpload(ctx, client, baseURL, sid, size)
		}
	}

	cancel()
	wg.Wait()
}

func doDownload(ctx context.Context, client *http.Client, baseURL *url.URL, sid string, size int64) {
	u := baseURL.JoinPath(fmt.Sprintf("/ndt/v8/session/%s/chunk/%d", sid, size))
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), http.NoBody)
	if err != nil {
		slog.Warn("download request failed", slog.Any("err", err))
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("download failed", slog.Any("err", err))
		return
	}
	bodyWrapper := slogging.NewReadCloser(resp.Body)
	defer bodyWrapper.Close()

	slog.Info("download chunk",
		slog.Int64("size", size),
		slog.Int("status", resp.StatusCode),
		slog.String("proto", resp.Proto),
	)

	buf := make([]byte, 1<<20) // 1 MiB
	io.CopyBuffer(io.Discard, bodyWrapper, buf)
}

func doUpload(ctx context.Context, client *http.Client, baseURL *url.URL, sid string, size int64) {
	u := baseURL.JoinPath(fmt.Sprintf("/ndt/v8/session/%s/chunk/%d", sid, size))
	body := io.LimitReader(infinite.Reader{}, size)
	req, err := http.NewRequestWithContext(ctx, "PUT", u.String(), body)
	if err != nil {
		slog.Warn("upload request failed", slog.Any("err", err))
		return
	}
	req.ContentLength = size

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("upload failed", slog.Any("err", err))
		return
	}
	defer resp.Body.Close()

	slog.Info("upload chunk",
		slog.Int64("size", size),
		slog.Int("status", resp.StatusCode),
		slog.String("proto", resp.Proto),
	)
}

// runProbes sends small probe requests at regular intervals until ctx is done.
func runProbes(ctx context.Context, client *http.Client, baseURL *url.URL, sid string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pid, err := uuid.NewV7()
			if err != nil {
				pid = uuid.New()
			}
			probeOnce(ctx, client, baseURL, sid, pid.String())
		}
	}
}

func probeOnce(ctx context.Context, client *http.Client, baseURL *url.URL, sid, pid string) {
	u := baseURL.JoinPath(fmt.Sprintf("/ndt/v8/session/%s/probe/%s", sid, pid))
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), http.NoBody)
	if err != nil {
		return
	}

	t0 := time.Now()
	resp, err := client.Do(req)
	rtt := time.Since(t0)
	if err != nil {
		return
	}
	resp.Body.Close()

	slog.Info("probe",
		slog.String("pid", pid),
		slog.Duration("rtt", rtt),
		slog.Int("status", resp.StatusCode),
	)
}
