package outbox

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Job is one claimed row. Opaque to the processor; fields are interpreted
// by the caller's Dispatch func.
type Job struct {
	ID          string
	Attempts    int
	PayloadJSON string
	Extra       any // caller-defined: target row, kind, etc.
}

// Result is what Dispatch returns.
type Result int

const (
	// ResultDelivered means the dispatch succeeded; processor calls MarkDelivered.
	ResultDelivered Result = iota
	// ResultRetry means the dispatch failed transiently; processor reschedules.
	ResultRetry
	// ResultDead means the dispatch failed permanently; processor calls MarkDead.
	ResultDead
)

// Funcs is the per-processor wiring.
type Funcs struct {
	Claim         func(ctx context.Context, batchSize int) ([]*Job, error)
	Dispatch      func(ctx context.Context, j *Job) (Result, string, error) // returns (result, errMsg, sentinel-err)
	MarkDelivered func(ctx context.Context, id string) error
	MarkFailed    func(ctx context.Context, id, lastErr string, nextAttemptAt time.Time) error
	MarkDead      func(ctx context.Context, id, lastErr string) error
}

// Config tunes the processor. Zero values use sensible defaults.
type Config struct {
	Workers      int // default 4
	BatchSize    int // default 16
	PollInterval time.Duration // default 1s
	MaxAttempts  int // default 8
	BackoffSteps []time.Duration
}

// Processor runs a worker pool that polls Claim, dispatches each Job,
// and updates state via MarkDelivered/MarkFailed/MarkDead.
type Processor struct {
	cfg    Config
	funcs  Funcs
	logger logger.Writer
	rng    *rand.Rand
	rngMu  sync.Mutex
}

// NewProcessor wires a Processor. Call Run to start it.
func NewProcessor(funcs Funcs, cfg Config, log logger.Writer) *Processor {
	if cfg.Workers == 0 {
		cfg.Workers = 4
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 16
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.BackoffSteps == nil {
		cfg.BackoffSteps = DefaultBackoffSteps
	}
	return &Processor{
		cfg:    cfg,
		funcs:  funcs,
		logger: log,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run blocks until ctx is cancelled. Spins up Workers goroutines, each
// long-polling Claim and dispatching results through the Funcs hooks.
func (p *Processor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx)
		}()
	}
	wg.Wait()
}

func (p *Processor) worker(ctx context.Context) {
	tick := time.NewTicker(p.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			jobs, err := p.funcs.Claim(ctx, p.cfg.BatchSize)
			if err != nil {
				if p.logger != nil {
					p.logger.Log(logger.Warn, "[outbox] claim: %v", err)
				}
				continue
			}
			for _, j := range jobs {
				p.dispatchOne(ctx, j)
			}
		}
	}
}

func (p *Processor) dispatchOne(ctx context.Context, j *Job) {
	res, errMsg, err := p.funcs.Dispatch(ctx, j)
	if err != nil && p.logger != nil {
		p.logger.Log(logger.Debug, "[outbox] dispatch error %s: %v", j.ID, err)
	}
	switch res {
	case ResultDelivered:
		if err := p.funcs.MarkDelivered(ctx, j.ID); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark delivered %s: %v", j.ID, err)
		}
	case ResultDead:
		if err := p.funcs.MarkDead(ctx, j.ID, errMsg); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark dead %s: %v", j.ID, err)
		}
	case ResultRetry:
		if j.Attempts+1 >= p.cfg.MaxAttempts {
			if err := p.funcs.MarkDead(ctx, j.ID, "max attempts: "+errMsg); err != nil && p.logger != nil {
				p.logger.Log(logger.Warn, "[outbox] mark dead (max attempts) %s: %v", j.ID, err)
			}
			return
		}
		p.rngMu.Lock()
		delay := NextAttemptDelay(j.Attempts+1, p.cfg.BackoffSteps, p.rng)
		p.rngMu.Unlock()
		next := time.Now().Add(delay)
		if err := p.funcs.MarkFailed(ctx, j.ID, errMsg, next); err != nil && p.logger != nil {
			p.logger.Log(logger.Warn, "[outbox] mark failed %s: %v", j.ID, err)
		}
	}
}
