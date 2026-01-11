package downloader

import (
	"context"
	"time"
)

// RetryConfig holds retry configuration.
type RetryConfig struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
}

// DefaultRetryConfig returns sensible defaults for retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  5 * time.Second,
		MaxDelay:      60 * time.Second,
		BackoffFactor: 2.0,
	}
}

// Retry executes a function with exponential backoff retry logic.
func Retry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var lastErr error
	var zero T

	delay := cfg.InitialDelay

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Wait with exponential backoff
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}

		// Increase delay for next attempt
		delay = time.Duration(float64(delay) * cfg.BackoffFactor)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	return zero, lastErr
}

// RetryWithCheck executes a function with retry, allowing custom retry decision.
func RetryWithCheck[T any](
	ctx context.Context,
	cfg RetryConfig,
	fn func() (T, error),
	shouldRetry func(error) bool,
) (T, error) {
	var lastErr error
	var zero T

	delay := cfg.InitialDelay

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if we should retry this error
		if !shouldRetry(err) {
			break
		}

		// Don't wait after the last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}

		delay = time.Duration(float64(delay) * cfg.BackoffFactor)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	return zero, lastErr
}
