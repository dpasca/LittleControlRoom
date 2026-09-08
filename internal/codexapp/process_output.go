package codexapp

import (
	"os"
	"os/exec"
	"sync"
	"time"
)

// appServerOutputDrainTimeout bounds how long the captured output of an exiting
// app-server may delay publishing that exit. A child that outlives its parent
// and keeps the pipe open must not stall the session forever.
const appServerOutputDrainTimeout = time.Second

// startWithOwnedOutputPipes starts cmd with stdout and stderr pipes whose
// lifetime this process controls. cmd.StdoutPipe and cmd.StderrPipe hand back
// pipes that cmd.Wait closes as soon as the process exits, so a reader goroutine
// scheduled late reads nothing at all — which loses exactly the stderr diagnosis
// that explains why an app-server died during startup.
func startWithOwnedOutputPipes(cmd *exec.Cmd) (stdout, stderr *os.File, err error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeFiles(stdoutReader, stdoutWriter)
		return nil, nil, err
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	if err := cmd.Start(); err != nil {
		closeFiles(stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, nil, err
	}
	// The child holds its own duplicates; drop the parent copies so the readers
	// see EOF once the process and its children are gone.
	closeFiles(stdoutWriter, stderrWriter)
	return stdoutReader, stderrReader, nil
}

// captureProcessOutput runs read against stream in its own goroutine, closing
// the stream and marking captured done when the stream ends.
func captureProcessOutput(captured *sync.WaitGroup, stream *os.File, read func(*os.File)) {
	captured.Add(1)
	go func() {
		defer captured.Done()
		defer func() { _ = stream.Close() }()
		read(stream)
	}()
}

// waitForOutputDrain blocks until the capture goroutines finish or timeout
// elapses, whichever comes first.
func waitForOutputDrain(captured *sync.WaitGroup, timeout time.Duration) {
	if captured == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		captured.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func closeFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
