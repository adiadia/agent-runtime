// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"sync"
	"time"

	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	initOnce sync.Once

	runsTotalCounter            *prometheus.CounterVec
	stepsTotalCounter           *prometheus.CounterVec
	stepExecutionDurationMetric prometheus.Histogram
	stepRetriesCounter          prometheus.Counter
	workerClaimLatencyMetric    prometheus.Histogram
)

// Init registers metrics on the default Prometheus registry exactly once.
func Init() {
	initOnce.Do(func() {
		runsTotalCounter = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "runs_total",
				Help: "Total number of run status transitions by status.",
			},
			[]string{"status"},
		)

		stepsTotalCounter = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "steps_total",
				Help: "Total number of step terminal updates by status.",
			},
			[]string{"status"},
		)

		stepExecutionDurationMetric = prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "step_execution_duration_seconds",
				Help:    "Duration of step executor calls in seconds.",
				Buckets: prometheus.DefBuckets,
			},
		)

		stepRetriesCounter = prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "step_retries_total",
				Help: "Total number of retried step attempts.",
			},
		)

		workerClaimLatencyMetric = prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "worker_claim_latency_seconds",
				Help:    "Latency of worker step claim queries in seconds.",
				Buckets: prometheus.DefBuckets,
			},
		)

		prometheus.MustRegister(
			runsTotalCounter,
			stepsTotalCounter,
			stepExecutionDurationMetric,
			stepRetriesCounter,
			workerClaimLatencyMetric,
		)

		// Ensure counter vectors are visible at /metrics before first increment.
		for _, status := range []domain.RunStatus{
			domain.RunPending,
			domain.RunRunning,
			domain.RunWaiting,
			domain.RunSuccess,
			domain.RunFailed,
			domain.RunCanceled,
		} {
			runsTotalCounter.WithLabelValues(string(status))
		}

		for _, status := range []domain.StepStatus{
			domain.StepPending,
			domain.StepRunning,
			domain.StepWaiting,
			domain.StepSuccess,
			domain.StepFailed,
			domain.StepCanceled,
		} {
			stepsTotalCounter.WithLabelValues(string(status))
		}
	})
}

func IncRunStatus(status string) {
	Init()
	runsTotalCounter.WithLabelValues(status).Inc()
}

func IncStepStatus(status string) {
	Init()
	stepsTotalCounter.WithLabelValues(status).Inc()
}

func ObserveStepExecutionDuration(d time.Duration) {
	Init()
	stepExecutionDurationMetric.Observe(d.Seconds())
}

func IncStepRetries() {
	Init()
	stepRetriesCounter.Inc()
}

func ObserveWorkerClaimLatency(d time.Duration) {
	Init()
	workerClaimLatencyMetric.Observe(d.Seconds())
}
