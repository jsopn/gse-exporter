// package gse talks to the georgian state electrosystem dashboard api
package gse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://www.gse.com.ge/apps/gsebackend/rest"

	maxBodySize = 8 << 20

	// plusHours=0 returns two empty lists, whatever minusHours says
	diagramMinusHours = 0
	diagramPlusHours  = 8
)

var tbilisi = time.FixedZone("+04", 4*60*60)

type Client struct {
	baseURL   string
	http      *http.Client
	userAgent string
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		http:      &http.Client{Timeout: 30 * time.Second},
		userAgent: "gse-exporter",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type Point struct {
	Time time.Time
	Data float64
}

type Diagram struct {
	Real     []Point
	Estimate []Point
}

type TypeSum struct {
	Hydro            *float64 `json:"hydroData"`
	Thermal          *float64 `json:"thermalData"`
	Wind             *float64 `json:"windPowerData"`
	Solar            *float64 `json:"solarData"`
	Import           *float64 `json:"importData"`
	Export           *float64 `json:"exportData"`
	TotalConsumption *float64 `json:"totalConsumption"`
	TotalGeneration  *float64 `json:"totalGeneration"`
}

type AreaSum struct {
	Russia         *float64 `json:"russiaSum"`
	RussiaSalkhino *float64 `json:"russiaSalkhinoSum"`
	RussiaJava     *float64 `json:"russiaJavaSum"`
	RussiaNakaduli *float64 `json:"russiaNakaduliSum"`
	Azerbaijan     *float64 `json:"azerbaijanSum"`
	Armenia        *float64 `json:"armeniaSum"`
	Turkey         *float64 `json:"turkeySum"`
}

type MapData struct {
	TypeSum TypeSum `json:"typeSum"`
	AreaSum AreaSum `json:"areaSum"`
}

// names off GSE's power flow page, russiaSum is Kavkasioni.
// value is signed, positive into georgia
type Link struct {
	Country string
	Name    string
	Value   *float64
}

func (a AreaSum) Links() []Link {
	return []Link{
		{"russia", "kavkasioni", a.Russia},
		{"russia", "salkhino", a.RussiaSalkhino},
		{"russia", "java", a.RussiaJava},
		{"russia", "nakaduli", a.RussiaNakaduli},
		{"azerbaijan", "mukhranis_veli_gardabani", a.Azerbaijan},
		{"armenia", "alaverdi", a.Armenia},
		{"turkey", "meskheti", a.Turkey},
	}
}

func (d *Diagram) LatestReal() (Point, bool) {
	if len(d.Real) == 0 {
		return Point{}, false
	}
	return d.Real[len(d.Real)-1], true
}

func (d *Diagram) EstimateAt(t time.Time) (float64, bool) {
	best, bestDiff := Point{}, time.Duration(-1)
	for _, p := range d.Estimate {
		diff := p.Time.Sub(t).Abs()
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = p, diff
		}
	}
	if bestDiff < 0 {
		return 0, false
	}
	return best.Data, true
}

// values are MW
func (c *Client) Map(ctx context.Context) (*MapData, error) {
	var out MapData
	if err := c.get(ctx, "/map", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// grid frequency in Hz
func (c *Client) Frequency(ctx context.Context) (*float64, error) {
	var out struct {
		Widget *float64 `json:"widget"`
	}
	if err := c.get(ctx, "/widget", nil, &out); err != nil {
		return nil, err
	}
	return out.Widget, nil
}

// values are MW
func (c *Client) Diagram(ctx context.Context) (*Diagram, error) {
	var out struct {
		Real     []rawPoint `json:"realDataList"`
		Estimate []rawPoint `json:"estimateDataList"`
	}
	q := url.Values{
		"minusHours": {strconv.Itoa(diagramMinusHours)},
		"plusHours":  {strconv.Itoa(diagramPlusHours)},
	}
	if err := c.get(ctx, "/diagram", q, &out); err != nil {
		return nil, err
	}
	recorded, err := c.points(out.Real)
	if err != nil {
		return nil, fmt.Errorf("realDataList: %w", err)
	}
	estimate, err := c.points(out.Estimate)
	if err != nil {
		return nil, fmt.Errorf("estimateDataList: %w", err)
	}
	return &Diagram{Real: recorded, Estimate: estimate}, nil
}

type rawPoint struct {
	Time string   `json:"time"`
	Data *float64 `json:"data"`
}

func (c *Client) points(raw []rawPoint) ([]Point, error) {
	out := make([]Point, 0, len(raw))
	for _, r := range raw {
		if r.Data == nil {
			continue
		}
		t, err := parseTime(r.Time)
		if err != nil {
			return nil, err
		}
		out = append(out, Point{Time: t, Data: *r.Data})
	}
	return out, nil
}

var naiveTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?`)

// the +0000 on the wire is a lie, it's tbilisi wall clock. honouring it puts
// every sample 4h into the future
func parseTime(s string) (time.Time, error) {
	m := naiveTime.FindString(s)
	if m == "" {
		return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
	}
	layout := "2006-01-02T15:04:05"
	if frac := len(m) - len(layout) - 1; frac > 0 {
		layout += "." + strings.Repeat("0", frac)
	}
	return time.ParseInLocation(layout, m, tbilisi)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("GET %s: unexpected status %s", path, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(dst); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}
