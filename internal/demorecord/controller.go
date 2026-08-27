package demorecord

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
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
	discovery *Discovery
	reference discoveryReference
}

func NewController() *Controller {
	return &Controller{}
}

func NewControllerWithDataDir(dataDir string) *Controller {
	return &Controller{discovery: NewDiscovery(dataDir)}
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
	return c.StartWithAssociation(path, Association{})
}

func (c *Controller) StartWithAssociation(path string, association Association) (string, error) {
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
	var reference discoveryReference
	if err == nil && c.discovery != nil {
		reference = discoveryReference{
			ID:            recorder.ID(),
			Status:        RecordingStatusActive,
			PackagePath:   recorder.Path(),
			OwnerPID:      os.Getpid(),
			StartedAt:     recorder.StartedAt().UTC(),
			FormatVersion: FormatVersion,
			Association:   association.Normalize(),
			UpdatedAt:     time.Now().UTC(),
		}
		if discoveryErr := c.discovery.writeReference(reference); discoveryErr != nil {
			closeErr := recorder.Close()
			err = errors.Join(discoveryErr, closeErr)
		}
	}

	c.mu.Lock()
	c.operation = controllerIdle
	if err == nil {
		c.recorder = recorder
		c.reference = reference
	}
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return recorder.Path(), nil
}

func (c *Controller) Associate(association Association) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.recorder == nil || c.discovery == nil || strings.TrimSpace(c.reference.ID) == "" {
		c.mu.Unlock()
		return nil
	}
	c.reference.Association = association.Normalize()
	c.reference.UpdatedAt = time.Now().UTC()
	reference := c.reference
	c.mu.Unlock()
	return c.discovery.writeReference(reference)
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
	reference := c.reference
	c.mu.Unlock()

	path := recorder.Path()
	var discoveryErr error
	if c.discovery != nil && strings.TrimSpace(reference.ID) != "" {
		reference.Status = RecordingStatusFinalizing
		reference.UpdatedAt = time.Now().UTC()
		discoveryErr = c.discovery.writeReference(reference)
	}
	recordingErr := recorder.Close()
	if c.discovery != nil && strings.TrimSpace(reference.ID) != "" {
		reference.Status = RecordingStatusFailed
		if reader, err := Open(path); err == nil {
			manifest := reader.Manifest()
			reference.DurationMS = manifest.DurationMS
			reference.FormatVersion = manifest.Version
			if manifest.CompletedAt != nil {
				reference.Status = RecordingStatusFinalized
				reference.CompletedAt = manifest.CompletedAt.UTC()
			}
		}
		reference.UpdatedAt = time.Now().UTC()
		discoveryErr = errors.Join(discoveryErr, c.discovery.writeReference(reference))
	}

	c.mu.Lock()
	c.operation = controllerIdle
	c.reference = discoveryReference{}
	c.mu.Unlock()
	return path, true, errors.Join(recordingErr, discoveryErr)
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
