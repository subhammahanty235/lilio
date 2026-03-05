package metrics

import "time"

type Collector interface {
	RecordPutObject(bucket string, sizeBytes int64, duration time.Duration)
	RecordGetObject(bucket string, sizeBytes int64, duration time.Duration)
	RecordDeleteObject(bucket string)

	RecordChunkStored(node string, sizeBytes int64)
	RecordChunkRetrieved(node string, sizeBytes int64)
	RecordChunkDeleted(node string)

	RecordQuorumWrite(success bool, nodesAttempted, nodesSucceeded int)
	RecordQuorumRead(success bool, nodesAttempted, nodesSucceeded int)
	RecordReadRepair(node string)

	RecordBackendHealth(node string, healthy bool)
	RecordBackendLatency(node string, operation string, duration time.Duration)

	RecordActiveConnections(count int)
	Type() string
	Handler() interface{}
}

type MetricType string

const (
	MetricTypePrometheus MetricType = "prometheus"
	MetricTypeNoop       MetricType = "nouse"
)

type Config struct {
	Enabled bool       `json:"enabled"`
	Type    MetricType `json:"type"`
	Path    string     `json:"path,omitempty"` // Default: /metrics
}

func DefaultConfig() Config {
	return Config{
		Enabled: true,
		Type:    MetricTypePrometheus,
		Path:    "/metrics",
	}
}
