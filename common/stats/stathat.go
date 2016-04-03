package stats

import (
	"gopkg.in/inconshreveable/log15.v2"
	"net/http"
	"net/url"
	"strconv"
)

func postStatHat(key, stat string, values url.Values) {
	values.Set("stat", stat)
	values.Set("ezkey", key)
	resp, err := http.PostForm("http://api.stathat.com/ez", values)
	if err != nil {
		log15.Error("couldn't post to StatHat", "err", err)
		return
	}
	if resp.StatusCode != 200 {
		log15.Error("bad status posting to StatHat", "status_code", resp.StatusCode)
	}
	resp.Body.Close()
}

type StatHatReporterConfig struct {
	Email  string
	Prefix string
}

func (shr *StatHatReporterConfig) report(stats []*collectedStat) {
	for _, s := range stats {
		for k, v := range s.Counters {
			n := shr.Prefix + " " + s.Name + " " + k
			values := url.Values{}
			values.Set("count", strconv.FormatInt(v, 10))
			postStatHat(shr.Email, n, values)
		}
		for k, v := range s.Values {
			n := shr.Prefix + " " + s.Name + " " + k
			values := url.Values{}
			values.Set("value", strconv.FormatFloat(v, 'f', 3, 64))
			postStatHat(shr.Email, n, values)
		}
		for k, v := range s.Timers {
			n := shr.Prefix + " " + s.Name + " " + k
			values := url.Values{}
			values.Set("value", strconv.FormatInt(int64(v), 10))
			postStatHat(shr.Email, n, values)
		}
	}
}
