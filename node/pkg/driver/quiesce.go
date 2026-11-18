package driver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

type Config struct {
	QuietPeriod      time.Duration
	MaxGiveUpTimeout time.Duration
	SocketPath       string // e.g., "/csi/quiesce.sock"
}

type Tracker struct {
	cfg            Config
	inFlight       atomic.Int32
	lastActivityNs atomic.Int64
	shuttingDown   atomic.Bool
}

func NewTracker(cfg Config) *Tracker {
	t := &Tracker{cfg: cfg}
	t.lastActivityNs.Store(time.Now().UnixNano())
	return t
}

// TryBeginUnstage checks if the driver is shutting down. If not, it increments the in-flight
// counter and returns a cleanup function to defer.
func (t *Tracker) TryBeginUnstage() (func(), error) {
	if t.shuttingDown.Load() {
		return nil, status.Error(codes.Unavailable, "CSI driver is shutting down; rejecting new NodeUnstageVolume")
	}

	t.inFlight.Add(1)
	t.lastActivityNs.Store(time.Now().UnixNano())

	// Double-check after incrementing to close the tiny race window where shuttingDown
	// flipped right before inFlight.Add(1).
	if t.shuttingDown.Load() {
		t.inFlight.Add(-1)
		return nil, status.Error(codes.Unavailable, "CSI driver is shutting down; rejecting new NodeUnstageVolume")
	}

	return func() {
		t.lastActivityNs.Store(time.Now().UnixNano())
		t.inFlight.Add(-1)
	}, nil
}

// MarkShuttingDown rejects any new NodeUnstageVolume requests from starting.
func (t *Tracker) MarkShuttingDown() {
	if t.shuttingDown.CompareAndSwap(false, true) {
		klog.Info("Shutdown gate closed: new NodeUnstageVolume calls will now be rejected")
	}
}

func (t *Tracker) WaitUntilQuiescent(ctx context.Context) error {
	// Ensure the shutdown gate is always closed before preStop returns,
	// regardless of whether quiescence was reached or the give-up timer fired.
	defer t.MarkShuttingDown()

	deadline := time.Now().Add(t.cfg.MaxGiveUpTimeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	klog.Infof("PreStop triggered: waiting for %v of NodeUnstageVolume quiet time (give-up timeout: %v)",
		t.cfg.QuietPeriod, t.cfg.MaxGiveUpTimeout)

	for {
		inFlight := t.inFlight.Load()
		lastAct := time.Unix(0, t.lastActivityNs.Load())
		quietFor := time.Since(lastAct)

		if inFlight == 0 && quietFor >= t.cfg.QuietPeriod {
			klog.Infof("NodeUnstageVolume quiesced (quiet for %v). Closing gate and proceeding with shutdown.",
				quietFor.Round(time.Millisecond))
			return nil
		}

		if time.Now().After(deadline) {
			klog.Warningf("PreStop give-up timeout (%v) reached with %d in-flight unstages (last activity %v ago). Closing gate and forcing shutdown.",
				t.cfg.MaxGiveUpTimeout, inFlight, quietFor.Round(time.Millisecond))
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *Tracker) StartUnixServer(ctx context.Context) error {
	_ = os.Remove(t.cfg.SocketPath)

	listener, err := net.Listen("unix", t.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", t.cfg.SocketPath, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/prestop", func(w http.ResponseWriter, r *http.Request) {
		// Use a background context bounded by MaxGiveUpTimeout so client disconnects
		// don't abort the quiescence check prematurely.
		waitCtx, cancel := context.WithTimeout(context.Background(), t.cfg.MaxGiveUpTimeout+5*time.Second)
		defer cancel()

		_ = t.WaitUntilQuiescent(waitCtx)
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		// Also close the gate if SIGTERM arrives directly (e.g., manual kill or preStop skipped)
		t.MarkShuttingDown()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = os.Remove(t.cfg.SocketPath)
	}()

	klog.Infof("Starting preStop Unix socket server on %s", t.cfg.SocketPath)
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("prestop unix server failed: %w", err)
	}
	return nil
}

func RunPreStopClient(socketPath string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}

	resp, err := client.Get("http://unix/prestop")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

