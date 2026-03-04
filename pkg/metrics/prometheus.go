package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PrometheusCollector struct {
	registry *prometheus.Registry

	// Object metrics
	objectsTotal    *prometheus.CounterVec
	objectSizeBytes *prometheus.HistogramVec
	requestDuration *prometheus.HistogramVec

	// Chunk metrics
	chunksStoredTotal    *prometheus.CounterVec
	chunksRetrievedTotal *prometheus.CounterVec
	chunksDeletedTotal   *prometheus.CounterVec

	// Quorum metrics
	quorumWriteTotal *prometheus.CounterVec
	quorumReadTotal  *prometheus.CounterVec
	readRepairsTotal *prometheus.CounterVec
	quorumNodesGauge *prometheus.GaugeVec

	// Backend metrics
	backendHealth  *prometheus.GaugeVec
	backendLatency *prometheus.HistogramVec

	// System metrics
	activeConnections prometheus.Gauge
}

func NewPrometheusCollector() *PrometheusCollector {
	registry := prometheus.NewRegistry()

	c := &PrometheusCollector{
		registry: registry,

		// Object metrics
		objectsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_objects_total",
				Help: "Total number of object operations",
			},
			[]string{"bucket", "operation"}, // operation: put, get, delete
		),

		objectSizeBytes: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "lilio_object_size_bytes",
				Help:    "Size of objects in bytes",
				Buckets: []float64{1024, 10240, 102400, 1048576, 10485760, 104857600}, // 1KB to 100MB
			},
			[]string{"bucket", "operation"},
		),

		requestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "lilio_request_duration_seconds",
				Help:    "Duration of requests in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10},
			},
			[]string{"bucket", "operation"},
		),

		// Chunk metrics
		chunksStoredTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_chunks_stored_total",
				Help: "Total chunks stored per node",
			},
			[]string{"node"},
		),

		chunksRetrievedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_chunks_retrieved_total",
				Help: "Total chunks retrieved per node",
			},
			[]string{"node"},
		),

		chunksDeletedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_chunks_deleted_total",
				Help: "Total chunks deleted per node",
			},
			[]string{"node"},
		),

		// Quorum metrics
		quorumWriteTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_quorum_write_total",
				Help: "Total quorum write operations",
			},
			[]string{"success"}, // "true" or "false"
		),

		quorumReadTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_quorum_read_total",
				Help: "Total quorum read operations",
			},
			[]string{"success"},
		),

		readRepairsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "lilio_read_repairs_total",
				Help: "Total read repair operations triggered",
			},
			[]string{"node"},
		),

		quorumNodesGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "lilio_quorum_nodes",
				Help: "Number of nodes in quorum operations",
			},
			[]string{"operation", "type"}, // type: attempted, succeeded
		),

		// Backend metrics
		backendHealth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "lilio_backend_health",
				Help: "Health status of storage backends (1=healthy, 0=unhealthy)",
			},
			[]string{"node"},
		),

		backendLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "lilio_backend_latency_seconds",
				Help:    "Latency of backend operations in seconds",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
			},
			[]string{"node", "operation"}, // operation: store, retrieve, delete
		),

		// System metrics
		activeConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "lilio_active_connections",
				Help: "Number of active connections",
			},
		),
	}
	registry.MustRegister(
		c.objectsTotal,
		c.objectSizeBytes,
		c.requestDuration,
		c.chunksStoredTotal,
		c.chunksRetrievedTotal,
		c.chunksDeletedTotal,
		c.quorumWriteTotal,
		c.quorumReadTotal,
		c.readRepairsTotal,
		c.quorumNodesGauge,
		c.backendHealth,
		c.backendLatency,
		c.activeConnections,
	)

	return c
}

func (c *PrometheusCollector) Type() string {
	return string(MetricTypePrometheus)
}

func (c *PrometheusCollector) Handler() interface{} {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

func (c *PrometheusCollector) RecordPutObject(bucket string, sizeBytes int64, duration time.Duration) {
	c.objectsTotal.WithLabelValues(bucket, "put").Inc()
	c.objectSizeBytes.WithLabelValues(bucket, "put").Observe(float64(sizeBytes))
	c.requestDuration.WithLabelValues(bucket, "put").Observe(duration.Seconds())
}

func (c *PrometheusCollector) RecordGetObject(bucket string, sizeBytes int64, duration time.Duration) {
	c.objectsTotal.WithLabelValues(bucket, "get").Inc()
	c.objectSizeBytes.WithLabelValues(bucket, "get").Observe(float64(sizeBytes))
	c.requestDuration.WithLabelValues(bucket, "get").Observe(duration.Seconds())
}

func (c *PrometheusCollector) RecordDeleteObject(bucket string) {
	c.objectsTotal.WithLabelValues(bucket, "delete").Inc()
}

func (c *PrometheusCollector) RecordChunkStored(node string, sizeBytes int64) {
	c.chunksStoredTotal.WithLabelValues(node).Inc()
}

func (c *PrometheusCollector) RecordChunkRetrieved(node string, sizeBytes int64) {
	c.chunksRetrievedTotal.WithLabelValues(node).Inc()
}

func (c *PrometheusCollector) RecordChunkDeleted(node string) {
	c.chunksDeletedTotal.WithLabelValues(node).Inc()
}

func (c *PrometheusCollector) RecordQuorumWrite(success bool, nodesAttempted, nodesSucceeded int) {
	successStr := "false"
	if success {
		successStr = "true"
	}
	c.quorumWriteTotal.WithLabelValues(successStr).Inc()
	c.quorumNodesGauge.WithLabelValues("write", "attempted").Set(float64(nodesAttempted))
	c.quorumNodesGauge.WithLabelValues("write", "succeeded").Set(float64(nodesSucceeded))
}

func (c *PrometheusCollector) RecordQuorumRead(success bool, nodesAttempted, nodesSucceeded int) {
	successStr := "false"
	if success {
		successStr = "true"
	}
	c.quorumReadTotal.WithLabelValues(successStr).Inc()
	c.quorumNodesGauge.WithLabelValues("read", "attempted").Set(float64(nodesAttempted))
	c.quorumNodesGauge.WithLabelValues("read", "succeeded").Set(float64(nodesSucceeded))
}

func (c *PrometheusCollector) RecordReadRepair(node string) {
	c.readRepairsTotal.WithLabelValues(node).Inc()
}

func (c *PrometheusCollector) RecordBackendHealth(node string, healthy bool) {
	val := 0.0
	if healthy {
		val = 1.0
	}
	c.backendHealth.WithLabelValues(node).Set(val)
}

func (c *PrometheusCollector) RecordBackendLatency(node string, operation string, duration time.Duration) {
	c.backendLatency.WithLabelValues(node, operation).Observe(duration.Seconds())
}

func (c *PrometheusCollector) RecordActiveConnections(count int) {
	c.activeConnections.Set(float64(count))
}

var _ Collector = (*PrometheusCollector)(nil)
