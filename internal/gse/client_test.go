package gse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const mapBody = `{"typeSum":{"timestamp":null,"hydroData":749.852661,"thermalData":77.006622,"windPowerData":18.172564,"solarData":0.0,"importData":49.75905,"exportData":0.0,"totalConsumption":897.767212,"totalGeneration":843.820557},"areaSum":{"russiaSum":0.0,"russiaSalkhinoSum":0.0,"russiaJavaSum":11.0,"russiaNakaduliSum":0.0,"azerbaijanSum":37.338985,"armeniaSum":0.0,"turkeySum":1.420065}}`

const diagramBody = `{"realDataList":[
{"time":"2026-07-25T09:39:00.000+0000","data":856.214844},
{"time":"2026-07-25T09:42:00.000+0000","data":897.767212},
{"time":"2026-07-25T09:45:00.000+0000","data":null}],
"estimateDataList":[
{"time":"2026-07-25T09:03:00.000+0000","data":1559.0},
{"time":"2026-07-25T09:42:00.000+0000","data":1724.0},
{"time":"2026-07-25T10:03:00.000+0000","data":1854.0}]}`

func testClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, WithHTTPClient(srv.Client()))
}

func TestDiagram(t *testing.T) {
	c := testClient(t, diagramBody)
	d, err := c.Diagram(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Real) != 2 {
		t.Fatalf("got %d real points, want the null one dropped", len(d.Real))
	}

	latest, ok := d.LatestReal()
	if !ok || latest.Data != 897.767212 {
		t.Fatalf("latest = %v, %v", latest.Data, ok)
	}
	// 09:42 stamped +0000 is Tbilisi time, ie 05:42 UTC
	if want := time.Date(2026, 7, 25, 5, 42, 0, 0, time.UTC); !latest.Time.Equal(want) {
		t.Errorf("latest time = %s, want %s", latest.Time.UTC(), want)
	}
	if est, ok := d.EstimateAt(latest.Time); !ok || est != 1724.0 {
		t.Errorf("estimate = %v, %v; want 1724", est, ok)
	}
}

func TestDiagramQuery(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.Write([]byte(`{"realDataList":[],"estimateDataList":[]}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := New(srv.URL).Diagram(context.Background()); err != nil {
		t.Fatal(err)
	}
	// plusHours=0 would come back as two empty lists
	if want := "minusHours=0&plusHours=8"; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
}

func TestEstimateAtNearest(t *testing.T) {
	base := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)
	d := &Diagram{Estimate: []Point{{base, 1000}, {base.Add(10 * time.Minute), 2000}}}
	if v, _ := d.EstimateAt(base.Add(9 * time.Minute)); v != 2000 {
		t.Errorf("got %v, want the nearer sample 2000", v)
	}
	if _, ok := (&Diagram{}).EstimateAt(base); ok {
		t.Error("empty estimate list should report no value")
	}
}

func TestMapLinks(t *testing.T) {
	m, err := testClient(t, mapBody).Map(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := *m.TypeSum.TotalConsumption; got != 897.767212 {
		t.Errorf("totalConsumption = %v", got)
	}

	want := map[string]string{
		"kavkasioni": "russia", "salkhino": "russia", "java": "russia", "nakaduli": "russia",
		"mukhranis_veli_gardabani": "azerbaijan", "alaverdi": "armenia", "meskheti": "turkey",
	}
	links := m.AreaSum.Links()
	if len(links) != len(want) {
		t.Fatalf("got %d links, want %d", len(links), len(want))
	}
	for _, l := range links {
		if want[l.Name] != l.Country {
			t.Errorf("link %q has country %q, want %q", l.Name, l.Country, want[l.Name])
		}
		if l.Name == "java" && *l.Value != 11.0 {
			t.Errorf("java = %v, want 11", *l.Value)
		}
	}
}

func TestFrequency(t *testing.T) {
	got, err := testClient(t, `{"widget":50.0}`).Frequency(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != 50.0 {
		t.Errorf("got %v, want 50", got)
	}

	got, err = testClient(t, `{"widget":null}`).Frequency(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for a null reading", *got)
	}
}

func TestRequestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	if _, err := New(srv.URL).Map(context.Background()); err == nil {
		t.Error("want an error on 502")
	}
	if _, err := testClient(t, "<html>not json</html>").Frequency(context.Background()); err == nil {
		t.Error("want an error on a non-json body")
	}
}

func TestParseTime(t *testing.T) {
	loc := tbilisi
	for _, tc := range []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"2026-07-25T09:42:00.000+0000", time.Date(2026, 7, 25, 9, 42, 0, 0, loc), false},
		{"2026-07-25T09:42:00", time.Date(2026, 7, 25, 9, 42, 0, 0, loc), false},
		{"2026-07-25T09:42:00.123456Z", time.Date(2026, 7, 25, 9, 42, 0, 123456000, loc), false},
		{"", time.Time{}, true},
		{"yesterday", time.Time{}, true},
	} {
		got, err := parseTime(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && !got.Equal(tc.want) {
			t.Errorf("%q: got %s, want %s", tc.in, got, tc.want)
		}
	}
}
