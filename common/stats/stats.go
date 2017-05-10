package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

type HTTPSubHandler interface {
	HTTPHandler(relativeUrl []string, w http.ResponseWriter, r *http.Request)
}

type Config struct {
	Interval float64 `json:"interval"` // seconds
	History  int     // minutes

	Log15      []string `json:"log15"`
	StatHat    *StatHatReporterConfig
	NewRelic   *NewRelicReporterConfig
	Statsd     *StatsdConfig
	GCStats    int  `json:"gc_stats"`    // seconds
	NewStatter bool `json:"new_statter"` // Statter using armon/go-metrics
	NoHostname bool `json:"no_hostname"` //Don't include hostname by default
}

type Statter interface {
	Inc(value int64, stat ...string)
	Gauge(value int64, stat ...string)
	Measure(value int64, stat ...string)
	Time(value time.Duration, stat ...string)
	NewTimer(stat ...string) *Timer
}

func New(config Config) Statter {
	if config.Interval == 0.0 {
		config.Interval = 60.0 // convenience
	}

	if !config.NewStatter {
		return NewMultiStatter(config)
	}
	return NewMetricsStatter(config)
}

func HTTPReturnJson(w http.ResponseWriter, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	res, err := json.Marshal(result)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		w.Write(res)
	}
}

// statsd like API on top of the map manipulation API.
type Timer struct {
	statter  Statter
	stat     []string
	start    time.Time
	measured bool
}

func newTimer(st Statter, stat ...string) *Timer {
	return &Timer{st, stat, time.Now(), false}
}

func (timer *Timer) Measure() {
	if timer.measured {
		return
	}

	timer.measured = true
	timer.statter.Time(time.Since(timer.start), timer.stat...)
}

type NilStatter struct{}

func (n *NilStatter) Inc(value int64, stat ...string)          {}
func (n *NilStatter) Gauge(value int64, stat ...string)        {}
func (n *NilStatter) Measure(value int64, stat ...string)      {}
func (n *NilStatter) Time(value time.Duration, stat ...string) {}
func (r *NilStatter) NewTimer(stat ...string) *Timer {
	return newTimer(r, stat...)
}

func whoami() string {
	a, _ := net.InterfaceAddrs()
	for i := range a {
		// is a textual representation of an IPv4 address
		z, _, err := net.ParseCIDR(a[i].String())
		if a[i].Network() == "ip+net" && err == nil && z.To4() != nil {
			if !bytes.Equal(z, net.ParseIP("127.0.0.1")) {
				return strings.Replace(fmt.Sprintf("%v", z), ".", "_", -1)
			}
		}
	}
	return "127_0_0_1" // shrug
}
