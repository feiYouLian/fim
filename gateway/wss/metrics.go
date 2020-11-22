package wss

import (
	"github.com/prometheus/client_golang/prometheus"
)

var metricsNamespace = "im"

const (
	msgDirectionUp   = "upload"
	msgDirectionPush = "push"
)

var (
	messageTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "messages_total",
		Help:      "The number of messages uploaded or pushed",
	}, []string{"id", "protocol_id", "direction"})
	clientTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "client_total",
		Help:      "The number of client logined in",
	}, []string{"id"})
	serverTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "server_total",
		Help:      "The number of logic server connected in",
	}, []string{"id"})
	msgDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Buckets:   []float64{0.05, 0.1, 0.5, 1},
		Name:      "msg_duration_seconds",
		Help:      "The duration of a message from received to pushed, which may be received from another gateway",
	}, []string{"id"})
	msgDurationSecondsSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace:  metricsNamespace,
		Subsystem:  "gw",
		Objectives: map[float64]float64{0.5: 0.03, 0.9: 0.01},
		Name:       "msg_duration_seconds_summary",
		Help:       "The duration of a message from received to pushed, which may be received from another gateway",
	}, []string{"id"})
	messageFlowBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "message_flow_bytes",
		Help:      "The bytes of traffic for each type of message",
	}, []string{"id", "protocol_id"})
	pushErrorTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "push_error_total",
		Help:      "The number of message push failed",
	}, []string{"id", "protocol_id"})

	loginFailTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "gw",
		Name:      "login_fail_total",
		Help:      "The number of login failed",
	}, []string{"id"})
)

func init() {
	prometheus.MustRegister(messageTotal)
	prometheus.MustRegister(clientTotal)
	prometheus.MustRegister(msgDurationSeconds)
	prometheus.MustRegister(messageFlowBytes)
	prometheus.MustRegister(msgDurationSecondsSummary)
	prometheus.MustRegister(pushErrorTotal)
	prometheus.MustRegister(loginFailTotal)
}
