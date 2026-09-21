// Package queue provides an in-memory build job queue with a single worker,
// backed by on-disk persistence so history survives a restart.
//
// A single worker is deliberate: pack.sh extracts the source ISO into a shared
// ubuntu-files/ directory inside the template dir, so two concurrent runs on the
// same template would corrupt each other. Parallelism, if needed later, must
// come from per-job isolated template copies, not from more workers here.
package queue

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"isofactory/internal/assets"
	"isofactory/internal/progress"
	"isofactory/internal/storage"
)

const tsLayout = "2006-01-02 15:04:05"

// Runner executes a build for the given asset selection, streaming logs to
// logOut and coarse progress to rep. It returns the produced ISO path and a
// cleanup func (removes the per-job workspace) to call after the ISO is
// relocated. builder.Builder satisfies it; tests use a fake.
type Runner interface {
	Run(ctx context.Context, sel assets.Selection, logOut io.Writer, rep progress.Reporter) (string, func(), error)
}

// Status is the lifecycle state of a build job.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// syncBuffer is a bytes.Buffer safe for concurrent Write (by the worker) and
// String (by log-polling readers). bytes.Buffer alone is not.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// Job is a single ISO build request and its result.
type Job struct {
	ID         string
	Sel        assets.Selection // versioned asset selection for this build
	OutputName string           // desired download filename, e.g. ubuntu24.04.4-...iso
	Status     Status
	Error      string
	ISOPath    string
	Progress   progress.Update // latest coarse progress (phase + percent)
	Queued     time.Time
	Started    time.Time
	Finished   time.Time

	// log holds the live build output for an active job. Reloaded jobs have an
	// empty buffer and read their log from disk instead.
	log      *syncBuffer
	fromDisk bool
}

// Log returns the accumulated in-memory build log for the job.
func (j *Job) Log() string { return j.log.String() }

// Queue holds jobs and runs them one at a time.
type Queue struct {
	b     Runner
	store *storage.Store
	mu    sync.RWMutex
	seq   int
	all   map[string]*Job
	ch    chan *Job
}

// New creates a Queue backed by the given runner and store, reloads any
// persisted history, and starts `workers` concurrent workers. Each build runs
// in its own isolated workspace (see builder.Run), so N workers can build in
// parallel safely. workers < 1 is treated as 1. A nil store disables persistence.
func New(b Runner, store *storage.Store, workers int) *Queue {
	if workers < 1 {
		workers = 1
	}
	q := &Queue{
		b:     b,
		store: store,
		all:   make(map[string]*Job),
		ch:    make(chan *Job, 128),
	}
	q.reload()
	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q
}

// reload rebuilds in-memory job state from persisted records. Any job left in
// queued/running state (the process died mid-build) is marked failed, since its
// worker no longer exists.
func (q *Queue) reload() {
	if q.store == nil {
		return
	}
	recs, err := q.store.LoadAll()
	if err != nil {
		return
	}
	for _, r := range recs {
		job := &Job{
			ID: r.ID,
			Sel: assets.Selection{
				OS:            r.OS,
				SystemVersion: r.SystemVersion,
				IncludeNVIDIA: r.IncludeNVIDIA,
				NVIDIAVersion: r.NVIDIAVersion,
				CUDAVersion:   r.CUDAVersion,
				IncludeOFED:   r.IncludeOFED,
				OFEDVersion:   r.OFEDVersion,
			},
			OutputName: r.OutputName,
			Status:     Status(r.Status),
			Error:      r.Error,
			ISOPath:    r.ISOPath,
			Progress:   progress.Update{Phase: progress.Phase(r.Status)},
			Queued:     parseTS(r.Queued),
			Started:    parseTS(r.Started),
			Finished:   parseTS(r.Finished),
			log:        &syncBuffer{},
			fromDisk:   true,
		}
		if job.Status == StatusRunning || job.Status == StatusQueued {
			job.Status = StatusFailed
			job.Error = "interrupted by backend restart"
			job.Progress = progress.Update{Phase: progress.PhaseFailed, Message: job.Error}
			if job.Finished.IsZero() {
				job.Finished = time.Now()
			}
			q.persist(job)
		}
		q.all[job.ID] = job
	}
}

// Submit enqueues a new build for the given asset selection and desired output
// filename, and returns a snapshot of the created job. A snapshot (not the live
// pointer) is returned because the worker may start mutating the job as soon as
// it is enqueued — callers must not read fields off the live job concurrently.
func (q *Queue) Submit(sel assets.Selection, outputName string) *Job {
	q.mu.Lock()
	q.seq++
	id := newID(q.seq)
	job := &Job{
		ID:         id,
		Sel:        sel,
		OutputName: outputName,
		Status:     StatusQueued,
		Progress:   progress.Update{Phase: progress.PhaseQueued, Message: "已排队"},
		Queued:     time.Now(),
		log:        &syncBuffer{},
	}
	q.all[id] = job
	// Snapshot under the lock, before the worker can touch the live job.
	snap := *job
	q.mu.Unlock()

	q.persist(job)
	q.ch <- job
	return &snap
}

// setProgress records the latest progress update on a job under the lock.
func (q *Queue) setProgress(id string, u progress.Update) {
	q.mu.Lock()
	if job, ok := q.all[id]; ok {
		job.Progress = u
	}
	q.mu.Unlock()
}

// Get returns a snapshot of a job by ID. The returned *Job is a copy taken
// under the lock, so callers can read its fields without racing the worker that
// mutates the live job. The copy shares the log buffer, which is itself
// concurrency-safe.
func (q *Queue) Get(id string) (*Job, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	j, ok := q.all[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// JobLog returns a job's log: the live in-memory buffer for active jobs, or the
// persisted log on disk for reloaded ones.
func (q *Queue) JobLog(id string) (string, bool) {
	q.mu.RLock()
	job, ok := q.all[id]
	q.mu.RUnlock()
	if !ok {
		return "", false
	}
	if s := job.Log(); s != "" {
		return s, true
	}
	if q.store != nil {
		return q.store.ReadLog(id), true
	}
	return "", true
}

// List returns snapshots of all jobs, newest first. Each entry is a copy taken
// under the lock (see Get) so callers never read fields the worker is mutating.
func (q *Queue) List() []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]*Job, 0, len(q.all))
	for _, j := range q.all {
		cp := *j
		out = append(out, &cp)
	}
	// newest first by queued time
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].Queued.After(out[i].Queued) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

func (q *Queue) worker() {
	for job := range q.ch {
		q.run(job)
	}
}

func (q *Queue) run(job *Job) {
	q.mu.Lock()
	job.Status = StatusRunning
	job.Started = time.Now()
	q.mu.Unlock()
	q.setProgress(job.ID, progress.Update{Phase: progress.PhaseResolving, Percent: -1, Message: "开始构建"})
	q.persist(job)

	// A build can be long; give it a generous ceiling rather than no limit.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// Tee build output to both the in-memory buffer (live polling) and disk
	// (durability). logWriter appends each chunk to the persisted log file.
	out := io.MultiWriter(job.log, &logWriter{q: q, id: job.ID})
	rep := progress.ReporterFunc(func(u progress.Update) { q.setProgress(job.ID, u) })
	iso, cleanup, err := q.b.Run(ctx, job.Sel, out, rep)

	// On success, relocate the ISO out of the per-job workspace into the store,
	// THEN clean up the workspace (order matters — StoreISO reads from it).
	if err == nil && q.store != nil {
		q.setProgress(job.ID, progress.Update{Phase: progress.PhaseArchiving, Percent: -1, Message: "归档产物"})
		if moved, mvErr := q.store.StoreISO(job.ID, iso); mvErr != nil {
			_, _ = io.WriteString(out, "warning: failed to archive ISO to per-job path: "+mvErr.Error()+"\n")
		} else {
			iso = moved
		}
	}
	if cleanup != nil {
		cleanup() // remove the per-job workspace
	}

	q.mu.Lock()
	job.Finished = time.Now()
	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
		job.Progress = progress.Update{Phase: progress.PhaseFailed, Message: err.Error()}
	} else {
		job.Status = StatusDone
		job.ISOPath = iso
		job.Progress = progress.Update{Phase: progress.PhaseDone, Percent: 100, Message: "完成"}
	}
	q.mu.Unlock()
	q.persist(job)
}

// persist writes the job's current metadata to the store (no-op if none).
func (q *Queue) persist(job *Job) {
	if q.store == nil {
		return
	}
	q.mu.RLock()
	r := storage.Record{
		ID:            job.ID,
		OS:            job.Sel.OS,
		SystemVersion: job.Sel.SystemVersion,
		IncludeNVIDIA: job.Sel.IncludeNVIDIA,
		NVIDIAVersion: job.Sel.NVIDIAVersion,
		CUDAVersion:   job.Sel.CUDAVersion,
		IncludeOFED:   job.Sel.IncludeOFED,
		OFEDVersion:   job.Sel.OFEDVersion,
		OutputName:    job.OutputName,
		Status:        string(job.Status),
		Error:         job.Error,
		ISOPath:       job.ISOPath,
		Queued:        fmtTS(job.Queued),
		Started:       fmtTS(job.Started),
		Finished:      fmtTS(job.Finished),
	}
	q.mu.RUnlock()
	_ = q.store.SaveRecord(r)
}

// logWriter appends build output chunks to the persisted log file.
type logWriter struct {
	q  *Queue
	id string
}

func (w *logWriter) Write(p []byte) (int, error) {
	if w.q.store != nil {
		_ = w.q.store.AppendLog(w.id, string(p))
	}
	return len(p), nil
}

func newID(seq int) string {
	return time.Now().Format("20060102-150405") + "-" + itoa(seq)
}

func fmtTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(tsLayout)
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation(tsLayout, s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
