package progress

import (
	"bytes"
	"strings"
	"testing"
)

func TestAptProgressWriter_CountsFetchLines(t *testing.T) {
	var log bytes.Buffer
	var last Update
	rep := ReporterFunc(func(u Update) { last = u })
	w := NewAptProgressWriter(&log, rep, PhaseDownloading, "下载常用工具包")

	io := "resolving dependency closure for group \"base\" (13 top-level packages)\n" +
		"closure has 4 packages; downloading into /cache/tools/base\n" +
		"获取:1 http://m/ubuntu resolute/main amd64 procps amd64 2:4.0.4 [710 kB]\n" +
		"Get:2 http://m/ubuntu resolute/main amd64 readline-common all 8.3-4 [61 kB]\n"
	if _, err := w.Write([]byte(io)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Log must pass through verbatim.
	if !strings.Contains(log.String(), "procps") || !strings.Contains(log.String(), "readline-common") {
		t.Fatalf("underlying log not teed: %q", log.String())
	}
	// 2 of 4 fetched → 50%.
	if last.Percent != 50 {
		t.Fatalf("percent = %v, want 50", last.Percent)
	}
	if !strings.Contains(last.Message, "(2/4)") {
		t.Fatalf("message = %q, want it to contain (2/4)", last.Message)
	}
	if last.Phase != PhaseDownloading {
		t.Fatalf("phase = %v", last.Phase)
	}
}

func TestAptProgressWriter_ResetsOnNewClosure(t *testing.T) {
	var last Update
	rep := ReporterFunc(func(u Update) { last = u })
	w := NewAptProgressWriter(&bytes.Buffer{}, rep, PhaseDownloading, "工具包")

	w.Write([]byte("closure has 2 packages; x\n获取:1 a\n获取:2 b\n"))
	if last.Percent != 100 {
		t.Fatalf("first closure percent = %v, want 100", last.Percent)
	}
	// New closure resets the counter.
	w.Write([]byte("closure has 4 packages; y\n获取:1 c\n"))
	if last.Percent != 25 {
		t.Fatalf("second closure percent = %v, want 25", last.Percent)
	}
}

func TestAptProgressWriter_PartialLines(t *testing.T) {
	var last Update
	rep := ReporterFunc(func(u Update) { last = u })
	w := NewAptProgressWriter(&bytes.Buffer{}, rep, PhaseDownloading, "x")

	// Feed a line split across writes; progress must only fire on newline.
	w.Write([]byte("closure has 2 pac"))
	w.Write([]byte("kages; z\n"))
	w.Write([]byte("获取:1 "))
	if last.Percent != 0 {
		t.Fatalf("percent before newline = %v, want 0 (total known, none fetched)", last.Percent)
	}
	w.Write([]byte("pkg [1 kB]\n"))
	if last.Percent != 50 {
		t.Fatalf("percent after fetch line = %v, want 50", last.Percent)
	}
}
