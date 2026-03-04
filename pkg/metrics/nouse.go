package metrics

import "time"

// NoopCollector implements Collector but does nothing
// Used when metrics are disabled
type NoopCollector struct{}

func NewNoopCollector() *NoopCollector {
	return &NoopCollector{}
}

func (c *NoopCollector) Type() string         { return string(MetricTypeNoop) }
func (c *NoopCollector) Handler() interface{} { return nil }

func (c *NoopCollector) RecordPutObject(bucket string, sizeBytes int64, duration time.Duration) {}
func (c *NoopCollector) RecordGetObject(bucket string, sizeBytes int64, duration time.Duration) {}
func (c *NoopCollector) RecordDeleteObject(bucket string)                                       {}

func (c *NoopCollector) RecordChunkStored(node string, sizeBytes int64)    {}
func (c *NoopCollector) RecordChunkRetrieved(node string, sizeBytes int64) {}
func (c *NoopCollector) RecordChunkDeleted(node string)                    {}

func (c *NoopCollector) RecordQuorumWrite(success bool, nodesAttempted, nodesSucceeded int) {}
func (c *NoopCollector) RecordQuorumRead(success bool, nodesAttempted, nodesSucceeded int)  {}
func (c *NoopCollector) RecordReadRepair(node string)                                       {}

func (c *NoopCollector) RecordBackendHealth(node string, healthy bool)                              {}
func (c *NoopCollector) RecordBackendLatency(node string, operation string, duration time.Duration) {}

func (c *NoopCollector) RecordActiveConnections(count int) {}

var _ Collector = (*NoopCollector)(nil)
