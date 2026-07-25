package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jsopn/gse-exporter/internal/collector"
	"github.com/jsopn/gse-exporter/internal/gse"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("gse-exporter", flag.ContinueOnError)
	var (
		listen       = fs.String("web.listen-address", ":9821", "address to serve metrics on")
		metricsPath  = fs.String("web.telemetry-path", "/metrics", "path to serve metrics on")
		baseURL      = fs.String("gse.base-url", gse.DefaultBaseURL, "base URL of the GSE REST API")
		pollInterval = fs.Duration("poll.interval", time.Minute, "how often to poll the GSE API")
		pollTimeout  = fs.Duration("poll.timeout", 15*time.Second, "budget for one poll of all endpoints")
		logLevel     = fs.String("log.level", "info", "debug, info, warn or error")
		showVersion  = fs.Bool("version", false, "print version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: gse-exporter [flags]\n\nflags (also settable as GSE_EXPORTER_<FLAG>, dots and dashes to underscores):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := applyEnv(fs); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("gse-exporter", buildVersion())
		return nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("log.level: %w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	client := gse.New(*baseURL,
		gse.WithUserAgent("gse-exporter/"+buildVersion()),
		gse.WithHTTPClient(&http.Client{Timeout: *pollTimeout}),
	)
	c := collector.New(client, collector.Config{
		PollInterval: *pollInterval,
		PollTimeout:  *pollTimeout,
	}, log)

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		c,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelError),
		ErrorHandling: promhttp.ContinueOnError,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><head><title>gse-exporter</title></head><body>
<h1>gse-exporter</h1><p>georgian state electrosystem, as prometheus metrics</p>
<p><a href=%q>metrics</a></p></body></html>`, *metricsPath)
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "address", *listen, "path", *metricsPath, "version", buildVersion())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	<-done
	return err
}

func applyEnv(fs *flag.FlagSet) error {
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	replacer := strings.NewReplacer(".", "_", "-", "_")
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if err != nil || explicit[f.Name] {
			return
		}
		key := "GSE_EXPORTER_" + replacer.Replace(strings.ToUpper(f.Name))
		v, ok := os.LookupEnv(key)
		if !ok {
			return
		}
		if e := f.Value.Set(v); e != nil {
			err = fmt.Errorf("%s=%q: %w", key, v, e)
		}
	})
	return err
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func buildInfo() prometheus.Collector {
	revision, goVersion := "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		goVersion = info.GoVersion
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
		}
	}
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gse_exporter_build_info",
		Help: "Build information of the running exporter.",
	}, []string{"version", "revision", "goversion"})
	g.WithLabelValues(buildVersion(), revision, goVersion).Set(1)
	return g
}
