// SPDX-License-Identifier: AGPL-3.0-or-later

package slogging

import (
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/bassosimone/2026-02-provlima/internal/humanize"
)

// Setup configures the default slog logger to write to os.Stdout.
// When format is "json", it uses slog.NewJSONHandler; otherwise
// it uses slog.NewTextHandler.
func Setup(format string) {
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(handler))
}

// interval is the interval between each print
const interval = 250 * time.Millisecond

// ReadCloser is an infinite [io.ReadCloser].
//
// Construct using [NewReadCloser].
type ReadCloser struct {
	delta int64
	rc    io.ReadCloser
	t0    time.Time
	tot   int64
	tprev time.Time
}

// NewReadCloser constructs a new [*ReadCloser].
func NewReadCloser(rc io.ReadCloser) *ReadCloser {
	now := time.Now()
	return &ReadCloser{
		rc:    rc,
		tprev: now,
		delta: 0,
		t0:    now,
		tot:   0,
	}
}

var _ io.ReadCloser = &ReadCloser{}

// Read implements [io.ReadCloser].
func (r *ReadCloser) Read(data []byte) (int, error) {
	count, err := r.rc.Read(data)
	r.delta += int64(count)
	r.tot += int64(count)
	now := time.Now()
	if now.Sub(r.tprev) >= interval {
		r.emit("chunk read", now)
		r.delta = 0
		r.tprev = now
	}
	return count, err
}

// Close implements [io.ReadCloser].
func (r *ReadCloser) Close() error {
	r.emit("chunk done", time.Now())
	return r.rc.Close()
}

func (r *ReadCloser) emit(event string, now time.Time) {
	slog.Info(
		event,
		slog.Time("timeNow", now),
		slog.String("speed", humanize.SI(maybeSpeed(r.tot, r.t0, now), "bit/s")),
	)
}

func maybeSpeed(count int64, since, until time.Time) (speed float64) {
	elapsed := until.Sub(since).Seconds()
	if elapsed > 0 {
		speed = (float64(count) * 8) / elapsed
	}
	return

}
