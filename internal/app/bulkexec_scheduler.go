package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"finallyshell/internal/monitor"
)

type bulkStatus string

const (
	bulkWaiting    bulkStatus = "waiting"
	bulkConnecting bulkStatus = "connecting"
	bulkRunning    bulkStatus = "running"
	bulkOK         bulkStatus = "ok"
	bulkFailed     bulkStatus = "failed"
	bulkTimeout    bulkStatus = "timeout"
	bulkCancelled  bulkStatus = "cancelled"
	bulkSkipped    bulkStatus = "skipped"
	bulkMaxWorkers            = 50
)

func (status bulkStatus) final() bool {
	return status != bulkWaiting && status != bulkConnecting && status != bulkRunning
}

type bulkTarget struct {
	id, name, endpoint string
	address, user      string
	port               int
}

type bulkRequest struct {
	targets     []bulkTarget
	script      string
	timeout     time.Duration
	concurrency int
	root, stop  bool
	keepScript  bool
}

type bulkResult struct {
	target         bulkTarget
	status         bulkStatus
	exit           int
	duration       time.Duration
	stdout, stderr string
	err            error
	truncated      bool
}

type bulkRunner func(context.Context, bulkTarget, bulkRequest, func()) bulkResult

func bulkSchedule(ctx context.Context, request bulkRequest, run bulkRunner, publish func(bulkResult)) {
	var workers sync.WaitGroup
	var gate sync.Mutex
	next, stopped := 0, false
	for range min(len(request.targets), max(1, min(bulkMaxWorkers, request.concurrency))) {
		workers.Go(func() {
			for {
				gate.Lock()
				if next == len(request.targets) || stopped || ctx.Err() != nil {
					gate.Unlock()
					return
				}
				target := request.targets[next]
				next++
				gate.Unlock()
				finished := bulkRunTarget(ctx, request, target, run, publish)
				if request.stop && finished.status != bulkOK {
					gate.Lock()
					stopped = true
					gate.Unlock()
				}
				publish(finished)
			}
		})
	}
	workers.Wait()
	status := bulkSkipped
	if ctx.Err() != nil {
		status = bulkCancelled
	}
	for _, target := range request.targets[next:] {
		publish(bulkResult{target: target, status: status, exit: -1})
	}
}

func bulkRunTarget(parent context.Context, request bulkRequest, target bulkTarget, run bulkRunner, publish func(bulkResult)) bulkResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, request.timeout)
	defer cancel()
	publish(bulkResult{target: target, status: bulkConnecting, exit: -1})
	result := run(ctx, target, request, func() {
		publish(bulkResult{target: target, status: bulkRunning, exit: -1})
	})
	result.target, result.duration = target, time.Since(start)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(result.err, context.DeadlineExceeded):
		result.status, result.exit = bulkTimeout, -1
	case parent.Err() != nil, errors.Is(result.err, monitor.ErrPrivilegeCancelled):
		result.status, result.exit = bulkCancelled, -1
	case result.err != nil || result.exit != 0:
		result.status = bulkFailed
	default:
		result.status = bulkOK
	}
	return result
}
