// package collector polls GSE and exposes the readings to prometheus
package collector

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/jsopn/gse-exporter/internal/gse"
)

// GSE reports MW, prometheus wants base units
const megawatt = 1e6

const namespace = "gse"

type Config struct {
	PollInterval time.Duration
	PollTimeout  time.Duration
}

type Collector struct {
	client *gse.Client
	cfg    Config
	log    *slog.Logger

	state  atomic.Pointer[state]
	polls  atomic.Uint64
	errors atomic.Uint64

	up               *prometheus.Desc
	frequency        *prometheus.Desc
	consumption      *prometheus.Desc
	estimate         *prometheus.Desc
	generation       *prometheus.Desc
	generationTotal  *prometheus.Desc
	imports          *prometheus.Desc
	exports          *prometheus.Desc
	flow             *prometheus.Desc
	dataTimestamp    *prometheus.Desc
	successTimestamp *prometheus.Desc
	pollDuration     *prometheus.Desc
	pollsTotal       *prometheus.Desc
	pollErrorsTotal  *prometheus.Desc
}

type payload struct {
	m           *gse.MapData
	frequency   *float64
	consumption *float64
	estimate    *float64
	dataTime    time.Time
	at          time.Time
}

type state struct {
	ok       bool
	duration time.Duration
	last     *payload
}

func New(client *gse.Client, cfg Config, log *slog.Logger) *Collector {
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(namespace, "", name), help, labels, nil)
	}
	return &Collector{
		client: client,
		cfg:    cfg,
		log:    log,

		up:               desc("up", "Whether the last poll of the GSE API succeeded."),
		frequency:        desc("frequency_hertz", "Grid frequency."),
		consumption:      desc("consumption_watts", "Country-wide electricity consumption."),
		estimate:         desc("consumption_estimate_watts", "Consumption planned by GSE for the current sample."),
		generation:       desc("generation_watts", "Electricity generation by source.", "source"),
		generationTotal:  desc("generation_total_watts", "Total electricity generation."),
		imports:          desc("import_watts", "Total electricity imported."),
		exports:          desc("export_watts", "Total electricity exported."),
		flow:             desc("interconnection_flow_watts", "Flow over a cross-border interconnection, positive into Georgia and negative out.", "country", "link"),
		dataTimestamp:    desc("data_timestamp_seconds", "Timestamp of the most recent recorded sample."),
		successTimestamp: desc("last_success_timestamp_seconds", "Timestamp of the last successful poll."),
		pollDuration:     desc("poll_duration_seconds", "Duration of the last poll."),
		pollsTotal:       desc("polls_total", "Polls of the GSE API."),
		pollErrorsTotal:  desc("poll_errors_total", "Polls of the GSE API that failed."),
	}
}

func (c *Collector) Run(ctx context.Context) {
	c.poll(ctx)
	t := time.NewTicker(c.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.poll(ctx)
		}
	}
}

func (c *Collector) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.PollTimeout)
	defer cancel()

	start := time.Now()
	c.polls.Add(1)

	p, err := c.fetch(ctx)
	took := time.Since(start)

	prev := c.state.Load()
	if err != nil {
		c.errors.Add(1)
		c.log.Error("poll failed", "err", err, "took", took)
		last := (*payload)(nil)
		if prev != nil {
			last = prev.last
		}
		c.state.Store(&state{ok: false, duration: took, last: last})
		return
	}

	c.state.Store(&state{ok: true, duration: took, last: p})
	c.log.Debug("polled", "took", took, "consumption_mw", deref(p.consumption), "frequency_hz", deref(p.frequency))
}

func (c *Collector) fetch(ctx context.Context) (*payload, error) {
	m, err := c.client.Map(ctx)
	if err != nil {
		return nil, err
	}
	freq, err := c.client.Frequency(ctx)
	if err != nil {
		return nil, err
	}
	d, err := c.client.Diagram(ctx)
	if err != nil {
		return nil, err
	}

	p := &payload{m: m, frequency: freq, consumption: m.TypeSum.TotalConsumption, at: time.Now()}
	if latest, ok := d.LatestReal(); ok {
		p.dataTime = latest.Time
		if p.consumption == nil {
			p.consumption = &latest.Data
		}
		if est, ok := d.EstimateAt(latest.Time); ok {
			p.estimate = &est
		}
	}
	return p, nil
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, ch)
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	counter := func(d *prometheus.Desc, v uint64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v))
	}
	counter(c.pollsTotal, c.polls.Load())
	counter(c.pollErrorsTotal, c.errors.Load())

	s := c.state.Load()
	if s == nil {
		gauge(ch, c.up, 0)
		return
	}
	gauge(ch, c.up, boolValue(s.ok))
	gauge(ch, c.pollDuration, s.duration.Seconds())

	p := s.last
	if p == nil {
		return
	}
	gauge(ch, c.successTimestamp, float64(p.at.UnixNano())/1e9)
	if !p.dataTime.IsZero() {
		gauge(ch, c.dataTimestamp, float64(p.dataTime.UnixNano())/1e9)
	}
	optional(ch, c.frequency, p.frequency, 1)
	optional(ch, c.consumption, p.consumption, megawatt)
	optional(ch, c.estimate, p.estimate, megawatt)

	t := p.m.TypeSum
	optional(ch, c.generation, t.Hydro, megawatt, "hydro")
	optional(ch, c.generation, t.Thermal, megawatt, "thermal")
	optional(ch, c.generation, t.Wind, megawatt, "wind")
	optional(ch, c.generation, t.Solar, megawatt, "solar")
	optional(ch, c.generationTotal, t.TotalGeneration, megawatt)
	optional(ch, c.imports, t.Import, megawatt)
	optional(ch, c.exports, t.Export, megawatt)

	for _, l := range p.m.AreaSum.Links() {
		optional(ch, c.flow, l.Value, megawatt, l.Country, l.Name)
	}
}

func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

// null upstream means no series, not a made up zero
func optional(ch chan<- prometheus.Metric, d *prometheus.Desc, v *float64, scale float64, labels ...string) {
	if v == nil {
		return
	}
	gauge(ch, d, *v*scale, labels...)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
