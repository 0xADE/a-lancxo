//go:build unix

package logdup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Setup duplicates os.Stdout and os.Stderr to the given file (append) while preserving the original streams.
// The returned cleanup restores std streams, waits for copy goroutines, and closes the log file.
func Setup(path string) (cleanup func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("logdup: mkdir: %w", err)
	}

	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, fmt.Errorf("logdup: open log: %w", err)
	}
	lw := &lockedWriter{w: logFile}

	dupOut, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("logdup: dup stdout: %w", err)
	}
	origStdout := os.NewFile(uintptr(dupOut), "stdout-dup")

	dupErr, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		_ = origStdout.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("logdup: dup stderr: %w", err)
	}
	origStderr := os.NewFile(uintptr(dupErr), "stderr-dup")

	prOut, pwOut, err := os.Pipe()
	if err != nil {
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("logdup: pipe stdout: %w", err)
	}

	prErr, pwErr, err := os.Pipe()
	if err != nil {
		_ = prOut.Close()
		_ = pwOut.Close()
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("logdup: pipe stderr: %w", err)
	}

	os.Stdout = pwOut
	os.Stderr = pwErr

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(origStdout, lw), prOut)
		_ = prOut.Close()
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(origStderr, lw), prErr)
		_ = prErr.Close()
	}()

	cleanup = func() {
		_ = pwOut.Close()
		_ = pwErr.Close()
		wg.Wait()
		os.Stdout = os.NewFile(uintptr(syscall.Stdout), "/dev/stdout")
		os.Stderr = os.NewFile(uintptr(syscall.Stderr), "/dev/stderr")
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
	}

	return cleanup, nil
}
