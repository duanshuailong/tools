package queue

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"isofactory/internal/assets"
	"isofactory/internal/progress"
	"isofactory/internal/storage"
)

// fakeRunner satisfies Runner without touching xorriso or the filesystem.
type fakeRunner struct {
	iso     string
	err     error
	delay   time.Duration
	cleaned *int32 // if non-nil, incremented when cleanup runs
	running *int32 // if non-nil, current in-flight Run count
	maxSeen *int32 // if non-nil, max simultaneous Run calls observed
}

func (f *fakeRunner) Run(_ context.Context, _ assets.Selection, logOut io.Writer, rep progress.Reporter) (string, func(), error) {
	if f.running != nil {
		n := atomic.AddInt32(f.running, 1)
		defer atomic.AddInt32(f.running, -1)
		for {
			m := atomic.LoadInt32(f.maxSeen)
			if n <= m || atomic.CompareAndSwapInt32(f.maxSeen, m, n) {
				break
			}
		}
	}
	_, _ = io.WriteString(logOut, "building...\n")
	rep.Report(progress.Update{Phase: progress.PhaseBuilding, Percent: 50})
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	cleanup := func() {
		if f.cleaned != nil {
			atomic.AddInt32(f.cleaned, 1)
		}
	}
	return f.iso, cleanup, f.err
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestBuildSuccessLifecycle(t *testing.T) {
	q := New(&fakeRunner{iso: "/out/new-ubuntu.iso"}, nil, 1)
	job := q.Submit(assets.Selection{IncludeNVIDIA: true}, "test.iso")

	waitFor(t, func() bool {
		j, _ := q.Get(job.ID)
		return j.Status == StatusDone
	})

	j, _ := q.Get(job.ID)
	if j.ISOPath != "/out/new-ubuntu.iso" {
		t.Errorf("ISOPath = %q", j.ISOPath)
	}
	if j.OutputName != "test.iso" {
		t.Errorf("OutputName = %q, want test.iso", j.OutputName)
	}
	if log, _ := q.JobLog(job.ID); log != "building...\n" {
		t.Errorf("log = %q", log)
	}
}

func TestBuildFailureLifecycle(t *testing.T) {
	q := New(&fakeRunner{err: errors.New("boom")}, nil, 1)
	job := q.Submit(assets.Selection{}, "test.iso")

	waitFor(t, func() bool {
		j, _ := q.Get(job.ID)
		return j.Status == StatusFailed
	})

	j, _ := q.Get(job.ID)
	if j.Error != "boom" {
		t.Errorf("Error = %q, want boom", j.Error)
	}
}

func TestPersistenceReloadMarksInterrupted(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a job that was mid-build when the process died.
	if err := store.SaveRecord(storage.Record{
		ID:     "20260920-120000-1",
		Status: string(StatusRunning),
		Queued: "2026-09-20 12:00:00",
	}); err != nil {
		t.Fatal(err)
	}

	// A fresh queue should reload it and mark it failed/interrupted.
	q := New(&fakeRunner{}, store, 1)
	j, ok := q.Get("20260920-120000-1")
	if !ok {
		t.Fatal("reloaded job not found")
	}
	if j.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", j.Status)
	}
	if j.Error == "" {
		t.Error("want interruption error, got empty")
	}
}

func TestCleanupCalledAfterBuild(t *testing.T) {
	var cleaned int32
	q := New(&fakeRunner{iso: "/out/x.iso", cleaned: &cleaned}, nil, 1)
	job := q.Submit(assets.Selection{}, "x.iso")
	waitFor(t, func() bool {
		j, _ := q.Get(job.ID)
		return j.Status == StatusDone
	})
	// cleanup runs synchronously in run() after the build; give it a beat.
	waitFor(t, func() bool { return atomic.LoadInt32(&cleaned) == 1 })
}

func TestConcurrentWorkers(t *testing.T) {
	var running, maxSeen int32
	// 3 workers, each fake build sleeps briefly so they overlap.
	q := New(&fakeRunner{iso: "x", delay: 80 * time.Millisecond, running: &running, maxSeen: &maxSeen}, nil, 3)
	for i := 0; i < 6; i++ {
		q.Submit(assets.Selection{}, "j.iso")
	}
	// Wait for all jobs to finish.
	waitFor(t, func() bool {
		done := 0
		for _, j := range q.List() {
			if j.Status == StatusDone {
				done++
			}
		}
		return done == 6
	})
	if m := atomic.LoadInt32(&maxSeen); m < 2 {
		t.Errorf("expected concurrent builds (max seen >= 2), got %d — workers not parallel", m)
	}
}

func TestListNewestFirst(t *testing.T) {
	q := New(&fakeRunner{iso: "x"}, nil, 1)
	first := q.Submit(assets.Selection{}, "a.iso")
	time.Sleep(2 * time.Millisecond)
	second := q.Submit(assets.Selection{}, "b.iso")

	list := q.List()
	if len(list) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(list))
	}
	if list[0].ID != second.ID || list[1].ID != first.ID {
		t.Errorf("not newest-first: %s, %s", list[0].ID, list[1].ID)
	}
}
