package collector

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/jsopn/gse-exporter/internal/gse"
)

const (
	mapBody     = `{"typeSum":{"timestamp":null,"hydroData":749.852661,"thermalData":77.006622,"windPowerData":18.172564,"solarData":0.0,"importData":49.75905,"exportData":0.0,"totalConsumption":897.767212,"totalGeneration":843.820557},"areaSum":{"russiaSum":0.0,"russiaSalkhinoSum":0.0,"russiaJavaSum":11.0,"russiaNakaduliSum":0.0,"azerbaijanSum":37.338985,"armeniaSum":0.0,"turkeySum":1.420065}}`
	diagramBody = `{"realDataList":[{"time":"2026-07-25T09:42:00.000+0000","data":897.767212}],"estimateDataList":[{"time":"2026-07-25T09:42:00.000+0000","data":1724.0}]}`
	widgetBody  = `{"widget":50.0}`
)

func fakeAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/map":
		w.Write([]byte(mapBody))
	case "/diagram":
		w.Write([]byte(diagramBody))
	case "/widget":
		w.Write([]byte(widgetBody))
	default:
		http.NotFound(w, r)
	}
}

func newCollector(t *testing.T, h http.HandlerFunc) *Collector {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := gse.New(srv.URL, gse.WithHTTPClient(srv.Client()))
	cfg := Config{PollInterval: time.Minute, PollTimeout: 5 * time.Second}
	return New(client, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// keys every series as "name,label=value"
func samples(t *testing.T, c *Collector) map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			for _, l := range m.GetLabel() {
				key += "," + l.GetName() + "=" + l.GetValue()
			}
			out[key] = sampleValue(m)
		}
	}
	return out
}

func sampleValue(m *dto.Metric) float64 {
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	return m.GetCounter().GetValue()
}

func compare(t *testing.T, got, want map[string]float64) {
	t.Helper()
	for k, w := range want {
		if v, ok := got[k]; !ok {
			t.Errorf("%s missing", k)
		} else if v != w {
			t.Errorf("%s = %v, want %v", k, v, w)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s = %v, unexpected", k, got[k])
		}
	}
}

func TestCollect(t *testing.T) {
	c := newCollector(t, fakeAPI)
	before := float64(time.Now().Unix())
	c.poll(context.Background())

	got := samples(t, c)
	if ts := got["gse_last_success_timestamp_seconds"]; ts < before {
		t.Errorf("last success = %v, want >= %v", ts, before)
	}
	delete(got, "gse_last_success_timestamp_seconds")
	delete(got, "gse_poll_duration_seconds")

	compare(t, got, map[string]float64{
		"gse_up":                              1,
		"gse_polls_total":                     1,
		"gse_poll_errors_total":               0,
		"gse_frequency_hertz":                 50,
		"gse_consumption_watts":               8.97767212e+08,
		"gse_consumption_estimate_watts":      1.724e+09,
		"gse_generation_watts,source=hydro":   7.49852661e+08,
		"gse_generation_watts,source=solar":   0,
		"gse_generation_watts,source=thermal": 7.7006622e+07,
		"gse_generation_watts,source=wind":    1.8172564e+07,
		"gse_generation_total_watts":          8.43820557e+08,
		"gse_import_watts":                    4.975905e+07,
		"gse_export_watts":                    0,
		// 09:42 stamped +0000 is Tbilisi time, ie 05:42 UTC
		"gse_data_timestamp_seconds":                                                      1.78495812e+09,
		"gse_interconnection_flow_watts,country=russia,link=kavkasioni":                   0,
		"gse_interconnection_flow_watts,country=russia,link=salkhino":                     0,
		"gse_interconnection_flow_watts,country=russia,link=java":                         1.1e+07,
		"gse_interconnection_flow_watts,country=russia,link=nakaduli":                     0,
		"gse_interconnection_flow_watts,country=azerbaijan,link=mukhranis_veli_gardabani": 3.7338985e+07,
		"gse_interconnection_flow_watts,country=armenia,link=alaverdi":                    0,
		"gse_interconnection_flow_watts,country=turkey,link=meskheti":                     1.420065e+06,
	})
}

func TestCollectBeforeFirstPoll(t *testing.T) {
	got := samples(t, newCollector(t, fakeAPI))
	compare(t, got, map[string]float64{
		"gse_up":                0,
		"gse_polls_total":       0,
		"gse_poll_errors_total": 0,
	})
}

func TestFailedPollKeepsLastReading(t *testing.T) {
	fail := false
	c := newCollector(t, func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fakeAPI(w, r)
	})
	c.poll(context.Background())
	fail = true
	c.poll(context.Background())

	got := samples(t, c)
	if got["gse_up"] != 0 || got["gse_poll_errors_total"] != 1 || got["gse_polls_total"] != 2 {
		t.Errorf("up=%v errors=%v polls=%v", got["gse_up"], got["gse_poll_errors_total"], got["gse_polls_total"])
	}
	if v := got["gse_consumption_watts"]; v != 8.97767212e+08 {
		t.Errorf("consumption = %v, want the reading from before the failure", v)
	}
}

func TestNullReadingsAreNotExported(t *testing.T) {
	c := newCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/map":
			w.Write([]byte(`{"typeSum":{"hydroData":null,"thermalData":1.0,"totalConsumption":null},"areaSum":{}}`))
		case "/diagram":
			w.Write([]byte(`{"realDataList":[],"estimateDataList":[]}`))
		case "/widget":
			w.Write([]byte(`{"widget":null}`))
		}
	})
	c.poll(context.Background())

	got := samples(t, c)
	for _, key := range []string{
		"gse_frequency_hertz",
		"gse_consumption_watts",
		"gse_data_timestamp_seconds",
		"gse_generation_watts,source=hydro",
		"gse_interconnection_flow_watts,country=turkey,link=meskheti",
	} {
		if v, ok := got[key]; ok {
			t.Errorf("%s = %v, want no series at all", key, v)
		}
	}
	if got["gse_generation_watts,source=thermal"] != 1e6 {
		t.Errorf("thermal = %v, want 1e6", got["gse_generation_watts,source=thermal"])
	}
}

func TestConsumptionFallsBackToDiagram(t *testing.T) {
	c := newCollector(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/map" {
			w.Write([]byte(`{"typeSum":{"totalConsumption":null},"areaSum":{}}`))
			return
		}
		fakeAPI(w, r)
	})
	c.poll(context.Background())

	if v := samples(t, c)["gse_consumption_watts"]; v != 8.97767212e+08 {
		t.Errorf("consumption = %v, want the newest diagram sample", v)
	}
}

func TestExportedFlowStaysNegative(t *testing.T) {
	c := newCollector(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/map" {
			w.Write([]byte(`{"typeSum":{"exportData":12.0},"areaSum":{"azerbaijanSum":-12.0}}`))
			return
		}
		fakeAPI(w, r)
	})
	c.poll(context.Background())

	got := samples(t, c)
	if v := got["gse_interconnection_flow_watts,country=azerbaijan,link=mukhranis_veli_gardabani"]; v != -1.2e+07 {
		t.Errorf("flow = %v, want -1.2e+07", v)
	}
}
