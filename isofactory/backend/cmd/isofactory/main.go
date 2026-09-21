// Command isofactory serves the ISO build platform HTTP API, and offers a
// `genpack` subcommand for one-off generic directory→ISO builds.
//
// Usage:
//
//	isofactory [serve] [flags]     # default: run the HTTP server
//	isofactory genpack [flags]     # build a bootable/data ISO from a directory
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"isofactory/internal/api"
	"isofactory/internal/assets"
	"isofactory/internal/builder"
	"isofactory/internal/doca"
	"isofactory/internal/generic"
	"isofactory/internal/nvapt"
	"isofactory/internal/queue"
	"isofactory/internal/storage"
	"isofactory/internal/toolpkgs"
)

func main() {
	// Subcommand dispatch: default is "serve".
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "genpack":
			runGenpack(os.Args[2:])
			return
		case "serve":
			os.Args = append(os.Args[:1], os.Args[2:]...) // drop the subcommand token
		}
	}
	runServe()
}

func runServe() {
	var (
		addr        = flag.String("addr", ":8080", "listen address")
		templateDir = flag.String("template", defaultTemplateDir(), "path to the template/ directory containing pack.sh")
		dataDir     = flag.String("data", "./data", "path for persisted job metadata and logs")
		assetsDir   = flag.String("assets", "", "path to the versioned asset store (default: <template>/../assets)")
		defaultOS   = flag.String("default-os", "ubuntu", "default base OS when a build omits it")
		defaultVer  = flag.String("default-version", "24.04.4", "default system version when a build omits it")
		workers     = flag.Int("workers", 3, "number of concurrent build workers (each builds in an isolated workspace)")
	)
	flag.Parse()

	absTemplate, err := filepath.Abs(*templateDir)
	if err != nil {
		log.Fatalf("resolve template dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(absTemplate, "pack.sh")); err != nil {
		log.Fatalf("pack.sh not found under %s (use -template to point at the template dir): %v", absTemplate, err)
	}

	store, err := storage.New(*dataDir)
	if err != nil {
		log.Fatalf("init storage at %s: %v", *dataDir, err)
	}

	assetRoot := *assetsDir
	if assetRoot == "" {
		assetRoot = filepath.Join(filepath.Dir(absTemplate), "assets")
	}
	assetStore, err := assets.New(assetRoot)
	if err != nil {
		log.Fatalf("init asset store at %s: %v", assetRoot, err)
	}
	// Keep the Ubuntu image list current by discovering versions from the mirror
	// (immediately + hourly); curated CUDA/OFED stay as fallback.
	stopRefresh := assetStore.StartAutoRefresh(1 * time.Hour)
	defer stopRefresh()

	b := builder.New(absTemplate, assetStore)
	b.DefaultOS = *defaultOS
	b.DefaultSystemVersion = *defaultVer
	// Per-job build workspaces live under the data dir (same filesystem as the
	// asset/deb caches, so hardlinks work).
	b.WorkRoot = filepath.Join(*dataDir, "work")
	if err := os.MkdirAll(b.WorkRoot, 0o755); err != nil {
		log.Fatalf("create work root %s: %v", b.WorkRoot, err)
	}
	// Version-keyed deb caches live under the data dir (shared across builds,
	// hardlinked into each per-job workspace).
	cacheDir := filepath.Join(*dataDir, "cache")
	keyringDir := filepath.Join(*dataDir, "keyrings")
	tm := toolpkgs.NewManager(filepath.Join(cacheDir, "tools"))
	nv := nvapt.NewManager(filepath.Join(cacheDir, "nvidia"), keyringDir)
	dc := doca.NewManager(filepath.Join(cacheDir, "doca"), keyringDir)
	// Let the builder fetch independently-selected driver+CUDA apt debs at build time.
	b.NV = nvAdapter{nv}
	// Let the builder download selected tool-package groups at build time.
	b.Tools = tm
	// Let the builder download DOCA-OFED when selected.
	b.DOCA = dc
	q := queue.New(b, store, *workers)
	srv := &api.Server{Q: q, B: b, T: tm, NV: nv, DC: dc}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(srv.Routes()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("IsoFactory backend listening on %s", *addr)
	log.Printf("  template=%s", absTemplate)
	log.Printf("  assets=%s (base image auto-downloaded if missing)", assetRoot)
	log.Printf("  default base=%s %s", *defaultOS, *defaultVer)
	log.Printf("WARNING: no authentication — run behind a trusted network or reverse proxy only")
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

// nvAdapter bridges builder.NVDownloader to *nvapt.Manager (their Selection
// types differ, so translate at the boundary).
type nvAdapter struct{ m *nvapt.Manager }

func (a nvAdapter) DownloadSync(ctx context.Context, sel builder.NVSelection, logOut io.Writer) (string, error) {
	return a.m.DownloadSync(ctx, nvapt.Selection{Driver: sel.Driver, Toolkit: sel.Toolkit}, logOut)
}

// runGenpack builds a generic bootable/data ISO from a source directory.
func runGenpack(argv []string) {
	fs := flag.NewFlagSet("genpack", flag.ExitOnError)
	var (
		src     = fs.String("src", "", "source directory whose contents become the ISO root (required)")
		out     = fs.String("out", "", "output ISO path (required)")
		vol     = fs.String("volume", "ISOFACTORY", "ISO volume label")
		boot    = fs.String("boot", "none", "boot mode: none | bios | uefi | both")
		biosImg = fs.String("bios-image", "", "El Torito BIOS boot image path (relative to src), e.g. isolinux/isolinux.bin")
		biosCat = fs.String("bios-catalog", "", "boot catalog path (relative to src); default boot.catalog")
		efiImg  = fs.String("efi-image", "", "EFI boot image path (relative to src), e.g. EFI/boot/bootx64.efi")
	)
	fs.Parse(argv)

	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "genpack: -src and -out are required")
		fs.Usage()
		os.Exit(2)
	}

	spec := generic.Spec{
		SourceDir:       *src,
		VolumeID:        *vol,
		Boot:            generic.BootMode(*boot),
		BIOSBootImage:   *biosImg,
		BIOSBootCatalog: *biosCat,
		EFIBootImage:    *efiImg,
	}
	got, err := generic.New().Run(context.Background(), spec, *out, os.Stdout)
	if err != nil {
		log.Fatalf("genpack failed: %v", err)
	}
	fmt.Printf("Done: %s\n", got)
}

// defaultTemplateDir guesses the template dir relative to the repo layout
// (backend/ and template/ are siblings).
func defaultTemplateDir() string {
	return filepath.Join("..", "template")
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
