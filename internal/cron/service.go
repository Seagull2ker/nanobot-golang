package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// JobKind describes the type of cron job.
type JobKind string

// Supported JobKind values.
const (
	// JobKindAt fires once at a specific wall-clock time.
	JobKindAt JobKind = "at"
	// JobKindEvery fires on a fixed interval.
	JobKindEvery JobKind = "every"
	// JobKindCron fires on a 6-field cron expression (with seconds).
	JobKindCron JobKind = "cron"
)

// Schedule defines when a job runs.
type Schedule struct {
	Kind         JobKind `json:"kind"`
	EverySeconds int     `json:"every_seconds,omitempty"`
	CronExpr     string  `json:"cron_expr,omitempty"`
	At           string  `json:"at,omitempty"` // ISO 8601 datetime
	TZ           string  `json:"tz,omitempty"`
	Message      string  `json:"message"`
	Name         string  `json:"name,omitempty"`
	Channel      string  `json:"channel,omitempty"`
	ChatID       string  `json:"chat_id,omitempty"`
	Deliver      bool    `json:"deliver"`
}

// Job represents a registered cron job.
type Job struct {
	ID       string    `json:"id"`
	Schedule Schedule  `json:"schedule"`
	NextRun  time.Time `json:"next_run"`
}

// CronService manages scheduled tasks.
type CronService struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	storePath string
	onFire    func(ctx context.Context, job *Job)
}

// New creates a CronService.
func New(storePath string, onFire func(ctx context.Context, job *Job)) *CronService {
	return &CronService{
		jobs:      make(map[string]*Job),
		storePath: storePath,
		onFire:    onFire,
	}
}

// AddJob registers a new cron job.
func (s *CronService) AddJob(schedule Schedule) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("cron_%d", time.Now().UnixNano())
	job := &Job{
		ID:       id,
		Schedule: schedule,
		NextRun:  s.nextRun(schedule),
	}
	s.jobs[id] = job
	return job, nil
}

// ListJobs returns all registered jobs.
func (s *CronService) ListJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var jobs []*Job
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	return jobs
}

// RemoveJob removes a job by ID.
func (s *CronService) RemoveJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("cron: job %s not found", id)
	}
	delete(s.jobs, id)
	return nil
}

// Run starts the cron service loop.
func (s *CronService) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.fireDue(ctx)
		}
	}
}

func (s *CronService) fireDue(ctx context.Context) {
	s.mu.Lock()
	var due []*Job
	now := time.Now()
	for _, j := range s.jobs {
		if now.After(j.NextRun) {
			due = append(due, j)
			j.NextRun = s.nextRun(j.Schedule)
		}
	}
	s.mu.Unlock()

	for _, j := range due {
		if s.onFire != nil && j.Schedule.Deliver {
			s.onFire(ctx, j)
		}
	}
}

func (s *CronService) nextRun(schedule Schedule) time.Time {
	now := time.Now()
	switch schedule.Kind {
	case JobKindEvery:
		return now.Add(time.Duration(schedule.EverySeconds) * time.Second)
	case JobKindAt:
		t, err := time.Parse(time.RFC3339, schedule.At)
		if err == nil && t.After(now) {
			return t
		}
		return now.Add(24 * time.Hour)
	case JobKindCron:
		return now.Add(60 * time.Second) // simplified
	default:
		return now.Add(60 * time.Second)
	}
}

// Save persists jobs to disk.
func (s *CronService) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jobs []*Job
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.storePath, data, 0644)
}

// Load restores jobs from disk.
func (s *CronService) Load() error {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range jobs {
		s.jobs[j.ID] = j
	}
	return nil
}
