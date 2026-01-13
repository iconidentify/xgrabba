package usbmanager

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DriveMonitor watches for USB drive events using polling.
type DriveMonitor struct {
	manager     *Manager
	logger      *slog.Logger
	subscribers map[chan DriveEvent]struct{}
	mu          sync.RWMutex
	cancel      context.CancelFunc
	running     bool
}

// NewDriveMonitor creates a new drive monitor.
func NewDriveMonitor(manager *Manager, logger *slog.Logger) *DriveMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &DriveMonitor{
		manager:     manager,
		logger:      logger,
		subscribers: make(map[chan DriveEvent]struct{}),
	}
}

// Subscribe returns a channel that receives drive events.
func (m *DriveMonitor) Subscribe() chan DriveEvent {
	ch := make(chan DriveEvent, 10)
	m.mu.Lock()
	m.subscribers[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (m *DriveMonitor) Unsubscribe(ch chan DriveEvent) {
	m.mu.Lock()
	delete(m.subscribers, ch)
	close(ch)
	m.mu.Unlock()
}

// Start begins monitoring for drive events.
func (m *DriveMonitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	go m.watchDevChanges(ctx)

	m.logger.Info("drive monitor started")
	return nil
}

// watchDevChanges polls /sys/block for changes
func (m *DriveMonitor) watchDevChanges(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	knownDrives := make(map[string]bool)

	// Initial scan
	drives, _ := m.manager.ScanDrives(ctx)
	for _, d := range drives {
		knownDrives[d.Device] = true
	}

	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.running = false
			m.mu.Unlock()
			m.logger.Info("drive monitor stopped")
			return
		case <-ticker.C:
			drives, err := m.manager.ScanDrives(ctx)
			if err != nil {
				continue
			}

			currentDrives := make(map[string]bool)

			for _, d := range drives {
				currentDrives[d.Device] = true
				if !knownDrives[d.Device] {
					m.logger.Info("drive added", "device", d.Device)
					m.broadcast(DriveEvent{
						Type:      EventAdded,
						Device:    d.Device,
						Timestamp: time.Now(),
					})
				}
			}

			for device := range knownDrives {
				if !currentDrives[device] {
					m.logger.Info("drive removed", "device", device)
					m.broadcast(DriveEvent{
						Type:      EventRemoved,
						Device:    device,
						Timestamp: time.Now(),
					})
				}
			}

			knownDrives = currentDrives
		}
	}
}

func (m *DriveMonitor) broadcast(event DriveEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for ch := range m.subscribers {
		select {
		case ch <- event:
		default:
			// Drop event if subscriber is slow
			m.logger.Warn("dropping event for slow subscriber")
		}
	}
}

// Stop stops the drive monitor.
func (m *DriveMonitor) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

// IsRunning returns whether the monitor is running.
func (m *DriveMonitor) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// GetCurrentDrives returns the current list of drives.
func (m *DriveMonitor) GetCurrentDrives(ctx context.Context) ([]Drive, error) {
	return m.manager.ScanDrives(ctx)
}
