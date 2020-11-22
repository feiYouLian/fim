package handler

import (
	"github.com/prometheus/client_golang/prometheus"
)

var metricsNamespace = "im"

var (
	handleDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: "lg",
		Buckets:   []float64{0.01, 0.05, 0.1, 1},
		Name:      "handle_duration_seconds",
		Help:      "The duration of done a message",
	}, []string{"id", "protocol_id"})

	handleDurationSecondsSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Namespace:  metricsNamespace,
		Subsystem:  "lg",
		Objectives: map[float64]float64{0.5: 0.03, 0.9: 0.01},
		Name:       "handle_duration_seconds_summary",
		Help:       "The duration of done a message",
	}, []string{"id", "protocol_id"})

	messageQueueSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: "lg",
		Name:      "message_queue_size",
		Help:      "The number of message queue",
	}, []string{"id", "protocol_id"})
)

func init() {
	prometheus.MustRegister(handleDurationSeconds)
	prometheus.MustRegister(handleDurationSecondsSummary)
	prometheus.MustRegister(messageQueueSize)
}
