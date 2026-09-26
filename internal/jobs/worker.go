package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Handler func(context.Context, Job) error

type Worker struct {
	Queue       *Queue
	Handlers    map[string]Handler
	Lease       time.Duration
	MaxAttempts int
	RetryDelay  func(attempt int) time.Duration
	OnError     func(error)
}

// ProcessOne handles one ready job. Errors remain visible in the queue and are
// returned to the caller; a stopped process leaves its lease for recovery.
func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	if w.Queue == nil || w.Lease <= 0 || w.MaxAttempts < 1 {
		return false, fmt.Errorf("invalid worker configuration")
	}
	job, err := w.Queue.Claim(ctx, w.Lease)
	if err != nil || job == nil {
		return false, err
	}
	handler := w.Handlers[job.Kind]
	if handler == nil {
		failure := fmt.Errorf("no handler for job kind %q", job.Kind)
		finalizeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.Queue.Fail(finalizeCtx, *job, failure.Error(), job.Attempts, 0); err != nil {
			return true, errors.Join(failure, err)
		}
		return true, failure
	}
	job.MaxAttempts = w.MaxAttempts

	jobCtx, cancelJob := context.WithCancel(ctx)
	done := make(chan struct{})
	renewDone := make(chan struct{})
	renewalErrors := make(chan error, 1)
	go func() {
		defer close(renewDone)
		period := w.Lease / 3
		if period <= 0 {
			period = time.Millisecond
		}
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if err := w.Queue.Renew(jobCtx, *job, w.Lease); err != nil {
					renewalErrors <- err
					cancelJob()
					return
				}
			}
		}
	}()
	handlerErr := handler(jobCtx, *job)
	close(done)
	<-renewDone
	cancelJob()
	select {
	case renewalErr := <-renewalErrors:
		return true, fmt.Errorf("renew job %d: %w", job.ID, renewalErr)
	default:
	}
	if ctx.Err() != nil {
		// Let another worker reclaim the job after its lease expires.
		return true, ctx.Err()
	}
	finalizeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if handlerErr == nil {
		return true, w.Queue.Complete(finalizeCtx, *job)
	}
	delay := defaultRetryDelay(job.Attempts)
	if w.RetryDelay != nil {
		delay = w.RetryDelay(job.Attempts)
	}
	if err := w.Queue.Fail(finalizeCtx, *job, handlerErr.Error(), w.MaxAttempts, delay); err != nil {
		return true, errors.Join(handlerErr, err)
	}
	return true, handlerErr
}

// Run polls until shutdown. Handlers remain responsible for idempotent effects.
func (w *Worker) Run(ctx context.Context, idlePause time.Duration) error {
	if idlePause <= 0 {
		return fmt.Errorf("idle pause must be positive")
	}
	for ctx.Err() == nil {
		processed, err := w.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil && w.OnError != nil {
			w.OnError(err)
		}
		if processed {
			continue
		}
		timer := time.NewTimer(idlePause)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

func defaultRetryDelay(attempt int) time.Duration {
	// Huey v1.47.0 retries after 60, 240, 960, 3840, and 15360 seconds.
	delay := time.Minute
	for i := 1; i < attempt && i < 5; i++ {
		delay *= 4
	}
	return delay
}
