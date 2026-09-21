// Package progress defines a small, dependency-free progress model shared by
// the builder (which emits phase/percent updates during fetch and packing) and
// the queue (which stores them on the job for the web UI to poll).
package progress

// Phase is a coarse build stage the UI can label and show a bar for.
type Phase string

const (
	PhaseQueued      Phase = "queued"
	PhaseResolving   Phase = "resolving"   // locating local assets / catalog lookup
	PhaseDownloading Phase = "downloading" // pulling a missing asset online
	PhaseBuilding    Phase = "building"    // pack.sh / xorriso running
	PhaseArchiving   Phase = "archiving"   // relocating the produced ISO
	PhaseDone        Phase = "done"
	PhaseFailed      Phase = "failed"
)

// Update is a single progress report.
type Update struct {
	Phase   Phase   `json:"phase"`
	Percent float64 `json:"percent"` // 0..100; -1 means indeterminate
	Message string  `json:"message"`
}

// Reporter receives progress updates. Implementations must be safe to call from
// the build goroutine.
type Reporter interface {
	Report(Update)
}

// ReporterFunc adapts a function to a Reporter.
type ReporterFunc func(Update)

// Report calls the underlying function.
func (f ReporterFunc) Report(u Update) {
	if f != nil {
		f(u)
	}
}

// Nop is a Reporter that discards updates; use when progress isn't needed.
var Nop Reporter = ReporterFunc(func(Update) {})
