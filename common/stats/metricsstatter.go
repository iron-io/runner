package stats

import (
	"os"
	"path"
	"strings"
	"time"

	"github.com/armon/go-metrics"
	"gopkg.in/inconshreveable/log15.v2"
)

func NewMetricsStatter(config Config) Statter {
	inm := metrics.NewInmemSink(10*time.Second, time.Minute)
	metrics.DefaultInmemSignal(inm)

	var fanout metrics.FanoutSink

	if config.Statsd != nil && config.Statsd.StatsdUdpTarget != "" {
		sink, err := metrics.NewStatsdSink(config.Statsd.StatsdUdpTarget)
		if err != nil {
			log15.Error("Couldn't create statsd reporter", "err", err)
		}
		fanout = append(fanout, sink)
	}
	prefix := "iron"
	if config.Statsd != nil && len(config.Statsd.Prefix) > 0 {
		prefix = config.Statsd.Prefix + ".iron"
	}
	metricsConfig := metrics.DefaultConfig(prefix)
	metricsConfig.EnableRuntimeMetrics = false // Use another metrics instance for runtime stats reporting
	metricsConfig.EnableHostname = false

	m := new(MetricsStatter)
	m.hostname = whoami()
	m.servicePrefix = path.Base(os.Args[0])

	if len(fanout) > 0 {
		if !config.NoHostname {
			metricsConfig.ServiceName = strings.Join([]string{prefix, m.servicePrefix, m.hostname}, ".")
		}
		fanout = append(fanout, inm)
		metrics.NewGlobal(metricsConfig, fanout)
	} else {
		metrics.NewGlobal(metricsConfig, inm)
	}

	if config.GCStats >= 0 {
		if config.GCStats == 0 {
			config.GCStats = 1
		}
		metricsConfig.EnableRuntimeMetrics = true
		metricsConfig.ProfileInterval = time.Duration(config.Interval * float64(time.Second))
		if len(fanout) > 0 {
			metricsConfig.ServiceName = strings.Join([]string{prefix, m.servicePrefix, m.hostname}, ".")
			metrics.New(metricsConfig, fanout)
		} else {
			metrics.New(metricsConfig, inm)
		}
	}

	log15.Info("Statter configured", "servicePrefix", m.servicePrefix)

	return m
}

type MetricsStatter struct {
	hostname      string
	servicePrefix string
}

func (m *MetricsStatter) template(stat ...string) []string {
	newstat := []string{m.servicePrefix}
	for _, token := range stat {
		switch token {
		case "@hostname":
			newstat = append(newstat, m.hostname)
		default:
			newstat = append(newstat, token)
		}
	}
	return newstat
}

func (m *MetricsStatter) Inc(value int64, stat ...string) {
	newstat := m.template(stat...)
	metrics.IncrCounter(newstat, float32(value))
}
func (m *MetricsStatter) Gauge(value int64, stat ...string) {
	newstat := m.template(stat...)
	metrics.SetGauge(newstat, float32(value))
}
func (m *MetricsStatter) Measure(value int64, stat ...string) {
	newstat := m.template(stat...)
	metrics.AddSample(newstat, float32(value))
}
func (m *MetricsStatter) Time(value time.Duration, stat ...string) {
	newstat := m.template(stat...)
	metrics.AddSample(newstat, float32(value.Nanoseconds()/1e6)) //Report ms to statsd
}
func (m *MetricsStatter) NewTimer(stat ...string) *Timer {
	return newTimer(m, m.template(stat...)...)
}
