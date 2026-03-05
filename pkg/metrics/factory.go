package metrics

import "fmt"

func NewCollector(cfg Config) (Collector, error) {
	if !cfg.Enabled {
		return NewNoopCollector(), nil
	}

	switch cfg.Type {
	case MetricTypePrometheus, "":
		// Default to Prometheus
		return NewPrometheusCollector(), nil

	case MetricTypeNoop:
		return NewNoopCollector(), nil

	default:
		return nil, fmt.Errorf("unknown metrics type: %s", cfg.Type)
	}
}

func MustNewCollector(cfg Config) Collector {
	c, err := NewCollector(cfg)
	if err != nil {
		panic(err)
	}
	return c
}
