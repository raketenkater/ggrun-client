package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type controlFailure struct {
	kind  string
	retry bool
	cause error
}

func (e *controlFailure) Error() string { return e.kind + ": " + e.cause.Error() }
func (e *controlFailure) Unwrap() error { return e.cause }

// Only evaluator requests may be repeated here. They return data and execute
// no actions. The outer hook context bounds total elapsed time across attempts.
func retryControl(ctx context.Context, evaluate func() (reviewVerdict, error)) (reviewVerdict, error) {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return reviewVerdict{}, err
		}
		verdict, err := evaluate()
		if ctx.Err() != nil {
			return reviewVerdict{}, ctx.Err()
		}
		if err == nil {
			return verdict, nil // allow, deny and ask are all final decisions.
		}
		var failure *controlFailure
		if !errors.As(err, &failure) || !failure.retry {
			return reviewVerdict{}, err
		}
		limit := 3
		if failure.kind == "transport" {
			limit = 2 // Preserve bounded dead-path detection across TLS reconnects.
		}
		if attempt >= limit {
			return reviewVerdict{}, fmt.Errorf("evaluator failed after %d attempts: %w", attempt, err)
		}
		timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return reviewVerdict{}, ctx.Err()
		case <-timer.C:
		}
	}
}
