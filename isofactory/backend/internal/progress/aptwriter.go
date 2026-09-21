package progress

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
)

// apt download output looks like:
//
//	closure has 259 packages; downloading into /path
//	获取:1 http://mirror/... pkg 1.2-3 [710 kB]
//	Get:2 http://mirror/... pkg 4.5-6 [61 kB]
//
// We learn the closure total from the first line and count each fetch line to
// derive a real percentage. Both the Chinese ("获取") and English ("Get") apt
// locales are matched, since the server locale isn't guaranteed.
var (
	reClosureTotal = regexp.MustCompile(`closure has (\d+) packages`)
	reFetchLine    = regexp.MustCompile(`^(?:获取|Get):\d+\s`)
)

// AptProgressWriter wraps an underlying log writer and, as apt download output
// streams through it, reports a real download percentage to a Reporter. It is
// line-buffered (writes need not be line-aligned) and reports under the given
// Phase. When it sees a new "closure has N packages" line, it resets its
// counter, so sequential closures (NVIDIA, then tool groups, then DOCA) each
// count 0→100 with the current group reflected in the message.
type AptProgressWriter struct {
	under   io.Writer
	rep     Reporter
	phase   Phase
	label   string // human phase label, prefixed to the "(x/N)" message
	partial []byte
	total   int
	count   int
}

// NewAptProgressWriter returns a writer that tees to under while reporting
// download progress to rep under phase. label is a short phase description
// (e.g. "下载常用工具包") shown alongside the running count.
func NewAptProgressWriter(under io.Writer, rep Reporter, phase Phase, label string) *AptProgressWriter {
	if rep == nil {
		rep = Nop
	}
	return &AptProgressWriter{under: under, rep: rep, phase: phase, label: label}
}

// Write tees p to the underlying writer and scans completed lines for progress.
func (w *AptProgressWriter) Write(p []byte) (int, error) {
	// Always pass through to the real log first.
	n, err := w.under.Write(p)

	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		line := w.partial[:i]
		w.partial = w.partial[i+1:]
		w.scan(line)
	}
	return n, err
}

func (w *AptProgressWriter) scan(line []byte) {
	if m := reClosureTotal.FindSubmatch(line); m != nil {
		if t, e := strconv.Atoi(string(m[1])); e == nil && t > 0 {
			w.total = t
			w.count = 0
			w.report()
		}
		return
	}
	if w.total > 0 && reFetchLine.Match(line) {
		w.count++
		if w.count > w.total {
			w.count = w.total
		}
		w.report()
	}
}

func (w *AptProgressWriter) report() {
	pct := -1.0
	if w.total > 0 {
		pct = float64(w.count) / float64(w.total) * 100
	}
	msg := w.label
	if w.total > 0 {
		msg = w.label + " (" + strconv.Itoa(w.count) + "/" + strconv.Itoa(w.total) + ")"
	}
	w.rep.Report(Update{Phase: w.phase, Percent: pct, Message: msg})
}
