package stats

import (
	"gopkg.in/inconshreveable/log15.v2"
)

type Log15Reporter struct {
}

func NewLog15Reporter() *Log15Reporter {
	return (&Log15Reporter{})
}

func (lr *Log15Reporter) report(stats []*collectedStat) {
	for _, s := range stats {
		var i []interface{}

		for k, v := range s.Counters {
			i = append(i, k, v)
		}
		for k, v := range s.Values {
			i = append(i, k, v)
		}
		for k, v := range s.Timers {
			i = append(i, k, v)
		}

		log15.Info(s.Name, i...)
	}
}
