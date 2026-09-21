// Package api exposes the build queue over HTTP.
//
// SECURITY NOTE: this server has NO authentication. It is intended to run on a
// trusted internal network or behind a reverse proxy that handles auth. Do not
// expose it directly to the public internet — the build and download endpoints
// would let anyone trigger builds and fetch produced ISOs. Add auth (reverse
// proxy, API token, or session) before any network-facing deployment.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"isofactory/internal/assets"
	"isofactory/internal/builder"
	"isofactory/internal/doca"
	"isofactory/internal/nvapt"
	"isofactory/internal/queue"
	"isofactory/internal/toolpkgs"
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	Q  *queue.Queue
	B  *builder.Builder
	T  *toolpkgs.Manager
	NV *nvapt.Manager
	DC *doca.Manager
}

// Routes registers all handlers and returns the mux.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/preflight", s.handlePreflight)
	mux.HandleFunc("GET /api/inventory", s.handleInventory)
	mux.HandleFunc("GET /api/catalog", s.handleCatalog)
	mux.HandleFunc("GET /api/default-name", s.handleDefaultName)
	mux.HandleFunc("POST /api/build", s.handleBuild)
	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /api/jobs/{id}/log", s.handleJobLog)
	mux.HandleFunc("GET /api/jobs/{id}/download", s.handleDownload)
	// Tool-package (offline apt) management.
	mux.HandleFunc("GET /api/toolpkgs", s.handleToolpkgs)
	mux.HandleFunc("POST /api/toolpkgs/{group}/download", s.handleToolpkgsDownload)
	mux.HandleFunc("GET /api/toolpkgs/{group}/log", s.handleToolpkgsLog)
	// Independent NVIDIA driver + CUDA toolkit selection (via NVIDIA apt repo).
	mux.HandleFunc("GET /api/nvapt/versions", s.handleNvaptVersions)
	mux.HandleFunc("GET /api/nvapt/status", s.handleNvaptStatus)
	mux.HandleFunc("POST /api/nvapt/download", s.handleNvaptDownload)
	mux.HandleFunc("GET /api/nvapt/log", s.handleNvaptLog)
	// DOCA-OFED (current OFED via NVIDIA DOCA apt repo).
	mux.HandleFunc("GET /api/doca/info", s.handleDocaInfo)
	mux.HandleFunc("GET /api/doca/status", s.handleDocaStatus)
	mux.HandleFunc("POST /api/doca/download", s.handleDocaDownload)
	mux.HandleFunc("GET /api/doca/log", s.handleDocaLog)
	return mux
}

type buildRequest struct {
	OS            string   `json:"os"`             // base OS, e.g. "ubuntu"; blank = default
	SystemVersion string   `json:"system_version"` // e.g. "24.04.4"; blank = default
	IncludeNVIDIA *bool    `json:"include_nvidia"`
	NVIDIAVersion string   `json:"nvidia_version"` // .run driver version (legacy path)
	CUDAVersion   string   `json:"cuda_version"`   // .run CUDA version (legacy path)
	NVDriver      string   `json:"nv_driver"`      // apt cuda-drivers version (independent path)
	NVToolkit     string   `json:"nv_toolkit"`     // apt cuda-toolkit package (independent path)
	IncludeOFED   *bool    `json:"include_ofed"`
	OFEDVersion   string   `json:"ofed_version"`
	OFEDSource    string   `json:"ofed_source"` // "mlnx" (legacy .tgz) | "doca" (current apt)
	ToolGroups    []string `json:"tool_groups"` // tool-package group names to bundle
	Name          string   `json:"name"`        // optional custom output filename; blank = auto
}

type jobResponse struct {
	ID         string  `json:"id"`
	OutputName string  `json:"output_name,omitempty"`
	Status     string  `json:"status"`
	Error      string  `json:"error,omitempty"`
	Phase      string  `json:"phase,omitempty"`
	Percent    float64 `json:"percent"`
	PhaseMsg   string  `json:"phase_msg,omitempty"`
	Queued     string  `json:"queued"`
	Started    string  `json:"started,omitempty"`
	Finished   string  `json:"finished,omitempty"`
	HasISO     bool    `json:"has_iso"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePreflight reports whether the environment is ready to build, so the UI
// can warn before a user submits a doomed job.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"ready": true}
	if err := s.B.Preflight(); err != nil {
		resp["ready"] = false
		resp["reason"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Report whether the default selection would download the base image, so the
	// UI can warn about the wait.
	if resolved, err := s.B.Resolve(assets.Selection{}); err == nil {
		for _, m := range resolved.Missing {
			if m.Kind == "base" && m.Downloadable {
				resp["will_fetch"] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleInventory reports the current build inputs (source ISO, deb groups,
// drivers) so the UI can show what will go into the ISO before a build.
func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.B.Inventory())
}

// handleToolpkgs lists the tool-package groups, their apt package lists, and the
// current download status of each (in-progress / done / how many debs on disk).
func (s *Server) handleToolpkgs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available": toolpkgs.Available(),
		"groups":    toolpkgs.DefaultGroups(),
		"status":    s.T.Statuses(),
	})
}

// handleToolpkgsDownload kicks off a background download of a group's apt
// closure into template/debs/<group>/ and regenerates the offline index.
func (s *Server) handleToolpkgsDownload(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	if err := s.T.Start(group); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "group": group})
}

// handleToolpkgsLog returns the accumulated download log for a group.
func (s *Server) handleToolpkgsLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s.T.Log(r.PathValue("group"))))
}

// handleNvaptVersions lists independently-selectable NVIDIA driver + CUDA
// toolkit versions from NVIDIA's apt repo (sets the repo up on first call, so
// this may take a few seconds).
func (s *Server) handleNvaptVersions(w http.ResponseWriter, r *http.Request) {
	if !nvapt.Available() {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	drivers, toolkits, err := s.NV.ListVersions(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"drivers":   drivers,
		"toolkits":  toolkits,
	})
}

// handleNvaptStatus returns the current driver/toolkit download status.
func (s *Server) handleNvaptStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.NV.Status())
}

type nvaptRequest struct {
	Driver  string `json:"driver"`  // cuda-drivers version, e.g. 580.65.06-1
	Toolkit string `json:"toolkit"` // cuda-toolkit package, e.g. cuda-toolkit-13-2
}

// handleNvaptDownload starts a background download of the chosen driver+toolkit.
func (s *Server) handleNvaptDownload(w http.ResponseWriter, r *http.Request) {
	var req nvaptRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := s.NV.Start(nvapt.Selection{Driver: req.Driver, Toolkit: req.Toolkit}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// handleNvaptLog returns the current/last driver/toolkit download log.
func (s *Server) handleNvaptLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s.NV.Log()))
}

// handleDocaInfo reports DOCA-OFED availability + the OFED version it provides.
func (s *Server) handleDocaInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.DC.Info(ctx))
}

// handleDocaStatus returns the current DOCA-OFED download status.
func (s *Server) handleDocaStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.DC.Status())
}

// handleDocaDownload starts a background DOCA-OFED download.
func (s *Server) handleDocaDownload(w http.ResponseWriter, r *http.Request) {
	if err := s.DC.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// handleDocaLog returns the current/last DOCA-OFED download log.
func (s *Server) handleDocaLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s.DC.Log()))
}

// handleCatalog lists everything the platform can download (base images, CUDA
// driver pairs, OFED versions) plus the deb package groups bundled from the
// template, so the UI can offer version choices as dropdowns.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	cat := s.B.Catalog()
	writeJSON(w, http.StatusOK, map[string]any{
		"base_images": cat.Images,
		"cuda":        cat.CUDA,
		"ofed":        cat.OFED,
		"deb_groups":  s.B.Inventory().DebGroups,
	})
}

// handleDefaultName previews the auto-composed output filename for the given
// selection (query params os/system_version/include_nvidia/nvidia_version/...),
// so the UI can show and let the user edit it before building.
func (s *Server) handleDefaultName(w http.ResponseWriter, r *http.Request) {
	sel := assets.Selection{
		OS:            r.URL.Query().Get("os"),
		SystemVersion: r.URL.Query().Get("system_version"),
		IncludeNVIDIA: queryBool(r, "include_nvidia", true),
		NVIDIAVersion: r.URL.Query().Get("nvidia_version"),
		CUDAVersion:   r.URL.Query().Get("cuda_version"),
		NVDriver:      r.URL.Query().Get("nv_driver"),
		NVToolkit:     r.URL.Query().Get("nv_toolkit"),
		IncludeOFED:   queryBool(r, "include_ofed", true),
		OFEDVersion:   r.URL.Query().Get("ofed_version"),
	}
	resolved, _ := s.B.Resolve(sel)
	writeJSON(w, http.StatusOK, map[string]any{
		"default_name": s.B.DefaultISOName(sel),
		"versions":     s.B.Versions(),
		"missing":      resolved.Missing,
	})
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req buildRequest
	if r.Body != nil {
		// Empty body is allowed; defaults apply.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	sel := selectionFrom(req)
	// A custom name is sanitized; a blank one falls back to the auto default.
	name := builder.SanitizeISOName(req.Name)
	if name == "" {
		name = s.B.DefaultISOName(sel)
	}
	job := s.Q.Submit(sel, name)
	writeJSON(w, http.StatusAccepted, toJobResponse(job))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.Q.List()
	out := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobResponse(j))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.Q.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, toJobResponse(job))
}

func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	log, ok := s.Q.JobLog(r.PathValue("id"))
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(log))
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	job, ok := s.Q.Get(r.PathValue("id"))
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if job.Status != queue.StatusDone || job.ISOPath == "" {
		http.Error(w, "ISO not ready for this job", http.StatusConflict)
		return
	}
	// Serve under the job's chosen name, falling back to the stored file's base.
	name := job.OutputName
	if name == "" {
		name = filepath.Base(job.ISOPath)
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, job.ISOPath)
}

func selectionFrom(req buildRequest) assets.Selection {
	sel := assets.Selection{
		OS:            req.OS,
		SystemVersion: req.SystemVersion,
		NVDriver:      req.NVDriver,
		NVToolkit:     req.NVToolkit,
		OFEDSource:    req.OFEDSource,
		ToolGroups:    req.ToolGroups,
	}
	// The apt-based independent path (nv_driver/nv_toolkit) and the legacy .run
	// path (nvidia_version/cuda_version) are mutually exclusive. When apt fields
	// are present, leave IncludeNVIDIA off so the store skips .run resolution —
	// the builder fetches the apt debs instead.
	if req.NVDriver == "" && req.NVToolkit == "" {
		sel.IncludeNVIDIA = req.IncludeNVIDIA == nil || *req.IncludeNVIDIA
		sel.NVIDIAVersion = req.NVIDIAVersion
		sel.CUDAVersion = req.CUDAVersion
	}
	// OFED source: "doca" fetches current OFED via the builder's DOCA path, so
	// the store must NOT try to resolve the legacy MLNX .tgz (IncludeOFED off).
	// "mlnx" (or blank) keeps the legacy .tgz path.
	if req.OFEDSource == "doca" {
		sel.IncludeOFED = false
	} else if req.IncludeOFED == nil || *req.IncludeOFED {
		sel.IncludeOFED = true
		sel.OFEDVersion = req.OFEDVersion
	}
	return sel
}

func toJobResponse(j *queue.Job) jobResponse {
	resp := jobResponse{
		ID:         j.ID,
		OutputName: j.OutputName,
		Status:     string(j.Status),
		Error:      j.Error,
		Phase:      string(j.Progress.Phase),
		Percent:    j.Progress.Percent,
		PhaseMsg:   j.Progress.Message,
		Queued:     j.Queued.Format("2006-01-02 15:04:05"),
		HasISO:     j.Status == queue.StatusDone && j.ISOPath != "",
	}
	if !j.Started.IsZero() {
		resp.Started = j.Started.Format("2006-01-02 15:04:05")
	}
	if !j.Finished.IsZero() {
		resp.Finished = j.Finished.Format("2006-01-02 15:04:05")
	}
	return resp
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// queryBool reads a boolean query param, returning def when absent. "false",
// "0", and "no" are false; anything else present is true.
func queryBool(r *http.Request, key string, def bool) bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	switch v {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}
