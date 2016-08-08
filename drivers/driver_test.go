package drivers_test

import (
	"testing"
	"time"

	"github.com/iron-io/titan/runner/drivers"
)

func TestDecimate(t *testing.T) {
	start := time.Now()
	stats := make([]drivers.Stat, 480)
	for i := range stats {
		stats[i] = drivers.Stat{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]uint64{"x": uint64(i)},
		}
		//		t.Log(stats[i])
	}

	stats = drivers.Decimate(240, stats)
	if len(stats) != 240 {
		t.Error("decimate function bad", len(stats))
	}

	//for i := range stats {
	//t.Log(stats[i])
	//}

	stats = make([]drivers.Stat, 700)
	for i := range stats {
		stats[i] = drivers.Stat{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]uint64{"x": uint64(i)},
		}
	}
	stats = drivers.Decimate(240, stats)
	if len(stats) != 240 {
		t.Error("decimate function bad", len(stats))
	}

	stats = make([]drivers.Stat, 300)
	for i := range stats {
		stats[i] = drivers.Stat{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]uint64{"x": uint64(i)},
		}
	}
	stats = drivers.Decimate(240, stats)
	if len(stats) != 240 {
		t.Error("decimate function bad", len(stats))
	}

	stats = make([]drivers.Stat, 300)
	for i := range stats {
		if i == 150 {
			// leave 1 large gap
			start = start.Add(20 * time.Minute)
		}
		stats[i] = drivers.Stat{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]uint64{"x": uint64(i)},
		}
	}
	stats = drivers.Decimate(240, stats)
	if len(stats) != 173 {
		t.Error("decimate function bad", len(stats))
	}
}
