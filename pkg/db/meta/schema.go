package meta

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"

const (
	columnIDStringKey uint16 = 1
	columnIDIntKey    uint16 = 2
	columnIDValue     uint16 = 3
	columnIDUpdatedAt uint16 = 4
)

// Tables returns every metadata table descriptor.
func Tables() []schema.Table {
	return defaultMetaRegistry.tables()
}
