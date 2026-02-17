// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bassosimone/2026-02-provlima/internal/humanize"
	"github.com/bassosimone/2026-02-provlima/internal/infinite"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

func serveMain(ctx context.Context, args []string) error {
	var (
		addressFlag = "127.0.0.1"
		certFlag    = "testdata/cert.pem"
		keyFlag     = "testdata/key.pem"
		portFlag    = "4443"
		staticFlag  = "static"
	)

	fset := vflag.NewFlagSet("ndt8 serve", vflag.ExitOnError)
	fset.StringVar(&addressFlag, 'A', "address", "Use the given IP `ADDRESS`.")
	fset.StringVar(&certFlag, 0, "cert", "Use `FILE` as the TLS certificate.")
	fset.AutoHelp('h', "help", "Print this help text and exit.")
	fset.StringVar(&keyFlag, 0, "key", "Use `FILE` as the TLS private key.")
	fset.StringVar(&portFlag, 'p', "port", "Use the given TCP `PORT`.")
	fset.StringVar(&staticFlag, 's', "static", "Serve static files from `DIR`.")
	runtimex.PanicOnError0(fset.Parse(args))

	sm := newSessionManager()

	mux := http.NewServeMux()
	mux.Handle("POST /ndt/v8/session", http.HandlerFunc(sm.handleCreateSession))
	mux.Handle("GET /ndt/v8/session/{sid}/chunk/{size}", http.HandlerFunc(sm.handleGetChunk))
	mux.Handle("PUT /ndt/v8/session/{sid}/chunk/{size}", http.HandlerFunc(sm.handlePutChunk))
	mux.Handle("GET /ndt/v8/session/{sid}/probe/{pid}", http.HandlerFunc(sm.handleProbe))
	mux.Handle("DELETE /ndt/v8/session/{sid}", http.HandlerFunc(sm.handleDeleteSession))

	if staticFlag != "" {
		slog.Info("serving static files", slog.String("dir", staticFlag))
		mux.Handle("GET /", http.FileServer(http.Dir(staticFlag)))
	}

	endpoint := net.JoinHostPort(addressFlag, portFlag)
	srv := &http.Server{
		Addr:    endpoint,
		Handler: mux,
		TLSConfig: &tls.Config{
			NextProtos: []string{"h2", "http/1.1"},
		},
		ConnState: func(conn net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				slog.Info("conn new", slog.String("remote", conn.RemoteAddr().String()))
			case http.StateClosed:
				slog.Info("conn closed", slog.String("remote", conn.RemoteAddr().String()))
			}
		},
	}

	go func() {
		defer srv.Close()
		<-ctx.Done()
	}()

	slog.Info("serving at", slog.String("addr", endpoint))
	err := srv.ListenAndServeTLS(certFlag, keyFlag)
	slog.Info("interrupted", slog.Any("err", err))

	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	runtimex.LogFatalOnError0(err)
	return nil
}

// sessionManager tracks active measurement sessions.
//
// TODO(bassosimone): sessions should expire.
type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]time.Time // sessionID → creation time
}

func newSessionManager() *sessionManager {
	return &sessionManager{sessions: make(map[string]time.Time)}
}

func (sm *sessionManager) createSession() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sid := runtimex.PanicOnError1(uuid.NewV7())
	id := sid.String()
	sm.sessions[id] = time.Now()
	return id
}

func (sm *sessionManager) sessionExists(sid string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.sessions[sid]
	return ok
}

func (sm *sessionManager) deleteSession(sid string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.sessions[sid]
	if ok {
		delete(sm.sessions, sid)
	}
	return ok
}

func (sm *sessionManager) handleDeleteSession(rw http.ResponseWriter, req *http.Request) {
	sid := req.PathValue("sid")
	if !sm.deleteSession(sid) {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	slog.Info("session deleted",
		slog.String("sid", sid),
		slog.String("remote", req.RemoteAddr),
	)
	rw.WriteHeader(http.StatusNoContent)
}

func (sm *sessionManager) handleCreateSession(rw http.ResponseWriter, req *http.Request) {
	sid := sm.createSession()
	slog.Info("session created",
		slog.String("sid", sid),
		slog.String("remote", req.RemoteAddr),
	)
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusCreated)
	json.NewEncoder(rw).Encode(map[string]string{"sessionID": sid})
}

func (sm *sessionManager) handleGetChunk(rw http.ResponseWriter, req *http.Request) {
	sid := req.PathValue("sid")
	if !sm.sessionExists(sid) {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	count, err := strconv.ParseInt(req.PathValue("size"), 10, 64)
	if err != nil || count <= 0 {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	alpn := ""
	if req.TLS != nil {
		alpn = req.TLS.NegotiatedProtocol
	}
	slog.Info("GET chunk",
		slog.String("sid", sid),
		slog.Int64("size", count),
		slog.String("proto", req.Proto),
		slog.String("alpn", alpn),
		slog.String("remote", req.RemoteAddr),
	)

	t0 := time.Now()
	bodyReader := io.LimitReader(infinite.Reader{}, count)
	rw.Header().Set("Content-Length", strconv.FormatInt(count, 10))
	rw.WriteHeader(http.StatusOK)
	buf := make([]byte, 1<<20) // 1 MiB
	written, _ := io.CopyBuffer(rw, bodyReader, buf)
	elapsed := time.Since(t0)

	slog.Info("GET chunk done",
		slog.String("sid", sid),
		slog.Int64("bytes", written),
		slog.Duration("elapsed", elapsed),
		slog.String("remote", req.RemoteAddr),
	)
}

func (sm *sessionManager) handlePutChunk(rw http.ResponseWriter, req *http.Request) {
	sid := req.PathValue("sid")
	if !sm.sessionExists(sid) {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	expectCount, err := strconv.ParseInt(req.PathValue("size"), 10, 64)
	if err != nil || expectCount <= 0 {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}

	alpn := ""
	if req.TLS != nil {
		alpn = req.TLS.NegotiatedProtocol
	}
	slog.Info("PUT chunk",
		slog.String("sid", sid),
		slog.Int64("expectSize", expectCount),
		slog.String("proto", req.Proto),
		slog.String("alpn", alpn),
		slog.String("remote", req.RemoteAddr),
	)

	t0 := time.Now()
	bodyReader := io.LimitReader(req.Body, expectCount)
	buf := make([]byte, 1<<20) // 1 MiB
	read, _ := io.CopyBuffer(io.Discard, bodyReader, buf)
	elapsed := time.Since(t0)

	speed := float64(read*8) / elapsed.Seconds()
	slog.Info("PUT chunk done",
		slog.String("sid", sid),
		slog.Int64("bytes", read),
		slog.Duration("elapsed", elapsed),
		slog.String("speed", humanize.SI(speed, "bit/s")),
		slog.String("remote", req.RemoteAddr),
	)
	rw.WriteHeader(http.StatusNoContent)
}

func (sm *sessionManager) handleProbe(rw http.ResponseWriter, req *http.Request) {
	sid := req.PathValue("sid")
	if !sm.sessionExists(sid) {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	pid := req.PathValue("pid")
	slog.Info("probe",
		slog.String("sid", sid),
		slog.String("pid", pid),
		slog.String("remote", req.RemoteAddr),
	)
	rw.WriteHeader(http.StatusNoContent)
}
