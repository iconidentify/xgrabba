package service

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
)

func TestEventService_Emit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 10}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Emit an event
	svc.EmitInfo(domain.EventCategoryTweet, "test", "test message", domain.EventMetadata{
		"tweet_id": "123",
	})

	// Check it was recorded
	events := svc.GetRecent(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Message != "test message" {
		t.Errorf("expected message 'test message', got '%s'", events[0].Message)
	}
	if events[0].Category != domain.EventCategoryTweet {
		t.Errorf("expected category tweet, got %s", events[0].Category)
	}
	if events[0].Severity != domain.EventSeverityInfo {
		t.Errorf("expected severity info, got %s", events[0].Severity)
	}
}

func TestEventService_RingBuffer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 5}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Emit 10 events
	for i := 0; i < 10; i++ {
		svc.EmitInfo(domain.EventCategorySystem, "test", "message "+string(rune('0'+i)), nil)
	}

	// Should only have last 5
	events := svc.GetRecent(10)
	if len(events) != 5 {
		t.Fatalf("expected 5 events (ring buffer size), got %d", len(events))
	}

	// Verify order (most recent first)
	// Last emitted was "message 9", first in result should be "message 9"
	if events[0].Message != "message 9" {
		t.Errorf("expected first event to be 'message 9', got '%s'", events[0].Message)
	}
	if events[4].Message != "message 5" {
		t.Errorf("expected last event to be 'message 5', got '%s'", events[4].Message)
	}
}

func TestEventService_Query_Filter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 100}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Emit various events
	svc.EmitInfo(domain.EventCategoryTweet, "tweet_svc", "tweet saved", nil)
	svc.EmitError(domain.EventCategoryNetwork, "http", "connection failed", nil)
	svc.EmitWarning(domain.EventCategoryDisk, "storage", "low disk space", nil)
	svc.EmitSuccess(domain.EventCategoryExport, "export_svc", "export complete", nil)

	// Query by severity
	errorSev := domain.EventSeverityError
	result, err := svc.Query(context.Background(), domain.EventQuery{
		Filter: domain.EventFilter{Severity: &errorSev},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 error event, got %d", len(result.Events))
	}
	if result.Events[0].Message != "connection failed" {
		t.Errorf("expected 'connection failed', got '%s'", result.Events[0].Message)
	}

	// Query by category
	exportCat := domain.EventCategoryExport
	result, err = svc.Query(context.Background(), domain.EventQuery{
		Filter: domain.EventFilter{Category: &exportCat},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 export event, got %d", len(result.Events))
	}

	// Query by search text
	result, err = svc.Query(context.Background(), domain.EventQuery{
		Filter: domain.EventFilter{SearchText: "disk"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event matching 'disk', got %d", len(result.Events))
	}
}

func TestEventService_SSE_Subscribe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 10}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Subscribe
	subID, ch := svc.Subscribe()
	if subID == 0 {
		t.Error("expected non-zero subscriber ID")
	}

	// Check stats
	if svc.SubscriberCount() != 1 {
		t.Errorf("expected 1 subscriber, got %d", svc.SubscriberCount())
	}

	// Emit event and receive it
	var wg sync.WaitGroup
	wg.Add(1)

	var received domain.Event
	go func() {
		defer wg.Done()
		select {
		case event := <-ch:
			received = event
		case <-time.After(time.Second):
			t.Error("timeout waiting for event")
		}
	}()

	svc.EmitInfo(domain.EventCategorySystem, "test", "SSE test", nil)
	wg.Wait()

	if received.Message != "SSE test" {
		t.Errorf("expected 'SSE test', got '%s'", received.Message)
	}

	// Unsubscribe
	svc.Unsubscribe(subID)
	if svc.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers after unsubscribe, got %d", svc.SubscriberCount())
	}
}

func TestEventService_ConcurrentEmit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 1000}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Emit events concurrently from multiple goroutines
	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				svc.EmitInfo(domain.EventCategorySystem, "test", "concurrent event", domain.EventMetadata{
					"goroutine": id,
					"iteration": j,
				})
			}
		}(i)
	}

	wg.Wait()

	// Verify all events were recorded
	stats := svc.Stats()
	if stats.BufferUsed != 1000 {
		t.Errorf("expected buffer to be full (1000), got %d", stats.BufferUsed)
	}
}

func TestEventService_AllSeverities(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 10}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	svc.EmitInfo(domain.EventCategorySystem, "test", "info message", nil)
	svc.EmitWarning(domain.EventCategorySystem, "test", "warning message", nil)
	svc.EmitError(domain.EventCategorySystem, "test", "error message", nil)
	svc.EmitSuccess(domain.EventCategorySystem, "test", "success message", nil)

	events := svc.GetRecent(10)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify severities (most recent first)
	expected := []domain.EventSeverity{
		domain.EventSeveritySuccess,
		domain.EventSeverityError,
		domain.EventSeverityWarning,
		domain.EventSeverityInfo,
	}
	for i, e := range events {
		if e.Severity != expected[i] {
			t.Errorf("event %d: expected severity %s, got %s", i, expected[i], e.Severity)
		}
	}
}

func TestEventService_Pagination(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, err := NewEventService(EventServiceConfig{RingBufferSize: 100}, logger)
	if err != nil {
		t.Fatalf("failed to create event service: %v", err)
	}
	defer svc.Close()

	// Emit 25 events
	for i := 0; i < 25; i++ {
		svc.EmitInfo(domain.EventCategorySystem, "test", "event "+string(rune('A'+i)), nil)
	}

	// Page 1: offset 0, limit 10
	result, _ := svc.Query(context.Background(), domain.EventQuery{Limit: 10, Offset: 0})
	if len(result.Events) != 10 {
		t.Errorf("page 1: expected 10 events, got %d", len(result.Events))
	}
	if !result.HasMore {
		t.Error("page 1: expected HasMore=true")
	}
	if result.Total != 25 {
		t.Errorf("expected total 25, got %d", result.Total)
	}

	// Page 2: offset 10, limit 10
	result, _ = svc.Query(context.Background(), domain.EventQuery{Limit: 10, Offset: 10})
	if len(result.Events) != 10 {
		t.Errorf("page 2: expected 10 events, got %d", len(result.Events))
	}
	if !result.HasMore {
		t.Error("page 2: expected HasMore=true")
	}

	// Page 3: offset 20, limit 10
	result, _ = svc.Query(context.Background(), domain.EventQuery{Limit: 10, Offset: 20})
	if len(result.Events) != 5 {
		t.Errorf("page 3: expected 5 events, got %d", len(result.Events))
	}
	if result.HasMore {
		t.Error("page 3: expected HasMore=false")
	}
}
