package metadata

import "fmt"

func NewStore(cfg Config) (MetadataStore, error) {
	switch cfg.Type {
	case StoreTypeLocal, "":
		if cfg.LocalConfig == nil {
			return nil, fmt.Errorf("local config is required for local store")
		}
		return NewLocalStore(cfg.LocalConfig.Path)

	case StoreTypeEtcd:
		if cfg.EtcdConfig == nil {
			return nil, fmt.Errorf("etcd config is required for etcd store")
		}
		return NewEtcdStore(cfg.EtcdConfig)

	case StoreTypeMemory:
		return NewMemoryStore(), nil

	default:
		return nil, fmt.Errorf("unknown store type: %s", cfg.Type)
	}

}

func NewLocalStoreSimple(basePath string) (MetadataStore, error) {
	return NewStore(Config{
		Type:        StoreTypeLocal,
		LocalConfig: &LocalConfig{Path: basePath},
	})
}

func NewEtcdStoreSimple(endpoints []string) (MetadataStore, error) {
	return NewStore(Config{
		Type: StoreTypeEtcd,
		EtcdConfig: &EtcdConfig{
			Endpoints: endpoints,
			Prefix:    "/lilio",
		},
	})
}
