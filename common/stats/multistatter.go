package stats

import (
	"time"

	"gopkg.in/inconshreveable/log15.v2"
)

type MultiStatter struct {
	statters []Statter
}

func NewMultiStatter(config Config) Statter {
	s := new(MultiStatter)

	var reporters []reporter
	if config.StatHat != nil && config.StatHat.Email != "" {
		reporters = append(reporters, config.StatHat)
	}

	if config.NewRelic != nil && config.NewRelic.LicenseKey != "" {
		// NR wants version?
		// can get it out of the namespace? roll it here?
		reporters = append(reporters, NewNewRelicReporter("1.0", config.NewRelic.LicenseKey))
	}

	if config.Log15 != nil {
		reporters = append(reporters, NewLogReporter())
	}

	if len(reporters) > 0 {
		ag := newAggregator(reporters)
		s.statters = append(s.statters, ag)
		go func() {
			for range time.Tick(time.Duration(config.Interval * float64(time.Second))) {
				ag.report(nil)
			}
		}()
	}

	if config.Statsd != nil && config.Statsd.StatsdUdpTarget != "" {
		std, err := NewStatsd(config.Statsd)
		if err == nil {
			s.statters = append(s.statters, std)
		} else {
			log15.Error("Couldn't create statsd reporter", "err", err)
		}
	}

	if len(reporters) == 0 && config.Statsd == nil && config.History == 0 {
		return &NilStatter{}
	}

	if config.GCStats >= 0 {
		if config.GCStats == 0 {
			config.GCStats = 1
		}
		go StartReportingMemoryAndGC(s, time.Duration(config.GCStats)*time.Second)
	}

	return s
}

func (s *MultiStatter) Inc(value int64, stat ...string) {
	for _, st := range s.statters {
		st.Inc(value, stat...)
	}
}

func (s *MultiStatter) Gauge(value int64, stat ...string) {
	for _, st := range s.statters {
		st.Gauge(value, stat...)
	}
}

func (s *MultiStatter) Measure(value int64, stat ...string) {
	for _, st := range s.statters {
		st.Measure(value, stat...)
	}
}

func (s *MultiStatter) Time(value time.Duration, stat ...string) {
	for _, st := range s.statters {
		st.Time(value, stat...)
	}
}

func (s *MultiStatter) NewTimer(stat ...string) *Timer {
	return newTimer(s, stat...)
}
