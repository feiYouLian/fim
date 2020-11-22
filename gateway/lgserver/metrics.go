package lgserver

import (
	"github.com/prometheus/client_golang/prometheus"
)

var metricsNamespace = "im"

const (
	msgDirectionUp   = "upload"
	msgDirectionPush = "push"
)

var (
	serverTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "server_total",
		Help:      "The number of logic server connected in",
	}, []string{"id"})
	writeLogicFailTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "write_logic_fail_total",
		Help:      "The number of message thrown when no logic server",
	}, []string{"id"})
)

func init() {
	prometheus.MustRegister(serverTotal)
	prometheus.MustRegister(writeLogicFailTotal)
}
