package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/ghchinoy/a2a-cli/internal/client"
	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/render"
)

// syncBuffer is a goroutine-safe bytes.Buffer: the command under test writes to it
// from the run goroutine while the test reads it, so access must be guarded.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// signalWriter is a goroutine-safe writer that closes seen the first time the
// accumulated output contains want. It lets a test learn — deterministically —
// that a specific line has been rendered, without racing on a timer.
type signalWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	want string
	seen chan struct{}
	done bool
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if !w.done && strings.Contains(w.buf.String(), w.want) {
		w.done = true
		close(w.seen)
	}
	return n, err
}

func (w *signalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// G1 — cancel mid-stream AFTER a taskId has been surfaced. This exercises the
// `errors.Is(serr, context.Canceled)` post-taskId branch of runSendStream, which
// coverage showed never ran. It is driven deterministically with a cancelable
// context (not an OS SIGINT): the SSE server emits a WORKING Task then blocks; the
// test waits until the client has rendered the Task line (so the taskId is known and
// the client is parked reading the next event), then cancels. The run must NOT map to
// success/terminal, and both the taskId and a resume hint must reach stderr so the
// interrupted task is recoverable (spec §7.3).
func TestStream_CancelAfterTaskId_ResumeHintAndNonZeroExit(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming:    true,
		events:       []map[string]any{taskEvent("t1", "c1", "TASK_STATE_WORKING")},
		emitThenHang: true,
		getTask:      taskJSON("t1", "c1", "TASK_STATE_COMPLETED"), // not reached: cancel precedes reconcile
	})

	cl, err := client.New(context.Background(), client.Options{ServiceURL: srv.URL, Transport: "jsonrpc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdout := &signalWriter{want: "Task:", seen: make(chan struct{})}
	stderr := &syncBuffer{}
	r := render.New(render.ModeText, stdout, stderr)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)

	done := make(chan error, 1)
	go func() {
		done <- runSendStream(ctx, flags, r, cl, nil, client.SendRequest{Text: "hi"}, srv.URL)
	}()

	// Wait until the Task line has been rendered — the client now holds the taskId and
	// is blocked reading the next event — then cancel.
	select {
	case <-stdout.seen:
	case <-time.After(10 * time.Second):
		t.Fatalf("Task line was never rendered; stdout=%q", stdout.String())
	}
	cancel()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runSendStream hung after cancel")
	}

	if clierr.ExitCode(runErr) == 0 {
		t.Fatalf("cancel after taskId must not map to success/terminal, got exit 0 (err=%v)", runErr)
	}
	se := stderr.String()
	if !strings.Contains(se, "task t1 created") {
		t.Errorf("the taskId must reach stderr so the task survives the cancel, got %q", se)
	}
	if !strings.Contains(se, "--task-id t1") {
		t.Errorf("a resume hint carrying the taskId must reach stderr, got %q", se)
	}
}
