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
	PendingInitQueueSize *prometheus.GaugeVec
	BlockProcessingDur   *prometheus.HistogramVec
	PoolInitDur          *prometheus.HistogramVec

	// --- Tier 3: Data & State Integrity ---
	PoolsInRegistry  *prometheus.GaugeVec
	PoolsInitialized *prometheus.CounterVec
}

// NewMetrics creates and registers all the Prometheus metrics for the system.
// It takes a prometheus.Registerer to allow for flexible registration (e.g., default vs. custom).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		// --- Tier 1 Metrics ---
		LastProcessedBlock: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "uniswap_system_last_processed_block",
			Help: "The block number of the last block successfully processed or skipped by the system.",
		}, []string{"system_name"}),

		ErrorsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "uniswap_system_errors_total",
			Help: "Total number of errors encountered by the system, labeled by error type.",
		}, []string{"system_name", "type"}),

		// --- Tier 2 Metrics ---
		PendingInitQueueSize: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "uniswap_system_pending_initialization_queue_size",
			Help: "The current number of pools waiting in the queue for asynchronous initialization.",
		}, []string{"system_name"}),

		BlockProcessingDur: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Name:    "uniswap_system_block_processing_duration_seconds",
			Help:    "A histogram of the time it takes to process a single block (the 'fast path').",
			Buckets: prometheus.DefBuckets, // Default buckets are a good starting point.
		}, []string{"system_name"}),

		PoolInitDur: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Name:    "uniswap_system_pool_initialization_duration_seconds",
			Help:    "A histogram of the time it takes for a batch of pending pools to be initialized.",
			Buckets: prometheus.DefBuckets,
		}, []string{"system_name"}),

		// --- Tier 3 Metrics ---
		PoolsInRegistry: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "uniswap_system_pools_in_registry_total",
			Help: "The total number of pools currently being tracked in the system's registry.",
		}, []string{"system_name"}),

		PoolsInitialized: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "uniswap_system_pools_initialized_total",
			Help: "A counter of pools successfully initialized and added to the registry.",
		}, []string{"system_name"}),
	}
}
