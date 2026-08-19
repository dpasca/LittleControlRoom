package demorecord

import (
	"fmt"
	"strings"
	"sync"
)

type controllerOperation string

const (
	controllerIdle     controllerOperation = ""
	controllerStarting controllerOperation = "starting"
	controllerStopping controllerOperation = "stopping"
)

// Controller lets a long-lived Bubble Tea model attach and detach recordings
// without replacing or restarting the model. Capture and interaction calls stay
// non-blocking; recorder creation and finalization are expected to run in a
// tea.Cmd rather than on the Bubble Tea update path.
type Controller struct {
	mu        sync.RWMutex
	recorder  *Recorder
	operation controllerOperation
}

func NewController() *Controller {
	return &Controller{}
}

func (c *Controller) Active() bool {
	return c.currentRecorder() != nil
}

func (c *Controller) Path() string {
	recorder := c.currentRecorder()
	if recorder == nil {
		return ""
	}
	return recorder.Path()
}

func (c *Controller) Start(path string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("demo recording controller is unavailable")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("recording path is required")
	}

	c.mu.Lock()
	if c.recorder != nil {
		activePath := c.recorder.Path()
		c.mu.Unlock()
		return activePath, fmt.Errorf("demo recording is already active: %s", activePath)
	}
	if c.operation != controllerIdle {
		operation := c.operation
		c.mu.Unlock()
		return "", fmt.Errorf("demo recording is already %s", operation)
	}
	c.operation = controllerStarting
	c.mu.Unlock()

	recorder, err := NewRecorder(path, RecorderOptions{})

	c.mu.Lock()
	c.operation = controllerIdle
	if err == nil {
		c.recorder = recorder
	}
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return recorder.Path(), nil
}

// Stop detaches the active recorder before waiting for disk finalization, so
// the render path remains responsive while the last chunk is flushed.
func (c *Controller) Stop() (string, bool, error) {
	if c == nil {
		return "", false, nil
	}

	c.mu.Lock()
	if c.operation != controllerIdle {
		operation := c.operation
		c.mu.Unlock()
		return "", false, fmt.Errorf("demo recording is already %s", operation)
	}
	recorder := c.recorder
	if recorder == nil {
		c.mu.Unlock()
		return "", false, nil
	}
	c.recorder = nil
	c.operation = controllerStopping
	c.mu.Unlock()

	path := recorder.Path()
	err := recorder.Close()

	c.mu.Lock()
	c.operation = controllerIdle
	c.mu.Unlock()
	return path, true, err
}

func (c *Controller) Close() error {
	_, _, err := c.Stop()
	return err
}

func (c *Controller) Capture(width, height int, view string) {
	if recorder := c.currentRecorder(); recorder != nil {
		recorder.Capture(width, height, view)
	}
}

func (c *Controller) MarkInteraction() {
	if recorder := c.currentRecorder(); recorder != nil {
		recorder.MarkInteraction()
	}
}

func (c *Controller) Err() error {
	if recorder := c.currentRecorder(); recorder != nil {
		return recorder.Err()
	}
	return nil
}

func (c *Controller) DroppedFrames() uint64 {
	if recorder := c.currentRecorder(); recorder != nil {
		return recorder.DroppedFrames()
	}
	return 0
}

func (c *Controller) currentRecorder() *Recorder {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.recorder
}
