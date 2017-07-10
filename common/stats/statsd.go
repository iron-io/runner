// Copyright 2016 Iron.io
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stats

import (
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
)

type keyCreator interface {
	// The return value of Key *MUST* never have a '.' at the end.
	Key(stat string) string
}

type theStatsdReporter struct {
	keyCreator
	client statsd.Statter
}

type prefixKeyCreator struct {
	parent   keyCreator
	prefixes []string
}

func (pkc *prefixKeyCreator) Key(stat string) string {
	prefix := strings.Join(pkc.prefixes, ".")

	if pkc.parent != nil {
		prefix = pkc.parent.Key(prefix)
	}

	if stat == "" {
		return prefix
	}

	if prefix == "" {
		return stat
	}

	return prefix + "." + stat
}

// The config.Prefix is sent before each message and can be used to set API
// keys. The prefix is used as the key prefix.
// If config is nil, creates a noop reporter.
//
//	st, e := NewStatsd(config, "ironmq")
//	st.Inc("enqueue", 1) -> Actually records to key ironmq.enqueue.
func NewStatsd(config *StatsdConfig) (*theStatsdReporter, error) {
	var client statsd.Statter
	var err error
	if config != nil {
		// 512 for now since we are sending to hostedgraphite over the internet.
		config.Prefix += "." + whoami()
		client, err = statsd.NewBufferedClient(config.StatsdUdpTarget, config.Prefix, time.Duration(config.Interval)*time.Second, 512)
	} else {
		client, err = statsd.NewNoopClient()
	}
	if err != nil {
		return nil, err
	}

	return &theStatsdReporter{keyCreator: &prefixKeyCreator{}, client: client}, nil
}

func (sr *theStatsdReporter) Inc(value int64, stat ...string) {
	sr.client.Inc(sr.keyCreator.Key(strings.Join(stat, ".")), value, 1)
}

func (sr *theStatsdReporter) Measure(delta int64, stat ...string) {
	sr.client.Timing(sr.keyCreator.Key(strings.Join(stat, ".")), delta, 1)
}

func (sr *theStatsdReporter) Time(delta time.Duration, stat ...string) {
	sr.client.TimingDuration(sr.keyCreator.Key(strings.Join(stat, ".")), delta, 1)
}

func (sr *theStatsdReporter) Gauge(value int64, stat ...string) {
	sr.client.Gauge(sr.keyCreator.Key(strings.Join(stat, ".")), value, 1)
}

func (sr *theStatsdReporter) NewTimer(stat ...string) *Timer {
	return newTimer(sr, strings.Join(stat, "."))
}
