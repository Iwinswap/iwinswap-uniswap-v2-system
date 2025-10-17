package uniswapv2

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// --- Prometheus Metrics Definition ---

// Metrics contains all the Prometheus metrics for the UniswapV2System.
// Encapsulating them in a struct keeps the main system struct clean and organized.
type Metrics struct {
	// --- Tier 1: Critical System Health & Liveness ---
	LastProcessedBlock *prometheus.GaugeVec
	ErrorsTotal        *prometheus.CounterVec

	// --- Tier 2: Performance & Bottleneck Identification ---
	PendingInitQueueSize   *prometheus.GaugeVec
	BlockProcessingDur     *prometheus.HistogramVec
	PoolInitDur            *prometheus.HistogramVec
	ReconciliationDuration *prometheus.HistogramVec
	PruningDuration        *prometheus.HistogramVec

	// --- Tier 3: Data & State Integrity ---
	PoolsInRegistry  *prometheus.GaugeVec
	PoolsInitialized *prometheus.CounterVec
	PoolsPruned      *prometheus.CounterVec
}

// NewMetrics creates and registers all the Prometheus metrics for the system.
// It takes a prometheus.Registerer to allow for flexible registration (e.g., default vs. custom).
func NewMetrics(reg prometheus.Registerer, systemName string) *Metrics {
	return &Metrics{
		// --- Tier 1 Metrics ---
		LastProcessedBlock: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_last_processed_block",
			Help:      "The block number of the last block successfully processed or skipped by the system.",
		}, []string{}),

		ErrorsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Subsystem: systemName,

			Name: "uniswap_system_errors_total",
			Help: "Total number of errors encountered by the system, labeled by error type.",
		}, []string{"type"}),

		// --- Tier 2 Metrics ---
		PendingInitQueueSize: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_pending_initialization_queue_size",
			Help:      "The current number of pools waiting in the queue for asynchronous initialization.",
		}, []string{}),

		BlockProcessingDur: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_block_processing_duration_seconds",
			Help:      "A histogram of the time it takes to process a single block (the 'fast path').",
			Buckets:   prometheus.DefBuckets, // Default buckets are a good starting point.
		}, []string{}),

		PoolInitDur: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_pool_initialization_duration_seconds",
			Help:      "A histogram of the time it takes for a batch of pending pools to be initialized.",
			Buckets:   prometheus.DefBuckets,
		}, []string{}),
		ReconciliationDuration: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_reconciliation_duration_seconds",
			Help:      "A histogram of the time it takes for the state reconciler to run a full cycle.",
			Buckets:   prometheus.DefBuckets,
		}, []string{}),
		PruningDuration: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: systemName,

			Name:    "uniswap_system_pruning_duration_seconds",
			Help:    "A histogram of the time it takes for the pruner to run a full cycle.",
			Buckets: prometheus.DefBuckets,
		}, []string{}),

		// --- Tier 3 Metrics ---
		PoolsInRegistry: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Subsystem: systemName,
			Name:      "uniswap_system_pools_in_registry_total",
			Help:      "The total number of pools currently being tracked in the system's registry.",
		}, []string{}),

		PoolsInitialized: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Subsystem: systemName,

			Name: "uniswap_system_pools_initialized_total",
			Help: "A counter of pools successfully initialized and added to the registry.",
		}, []string{}),
	}
}
