package hub

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var metricsNamespace = "fim"

var (
	writeGwFailTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "write_gw_fail_total",
		Help:      "The number of write gateway failed",
	}, []string{"id", "gw_id"})
)

func init() {
	prometheus.MustRegister(writeGwFailTotal)
}

func debugListen(host string) {
	http.Handle("/metrics", promhttp.Handler())

	_ = http.ListenAndServe(host, nil)
}
