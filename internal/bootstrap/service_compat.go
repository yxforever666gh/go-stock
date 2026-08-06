package bootstrap

import (
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type compatibilityServiceAdapter struct {
	main *gorm.DB
}

func newCompatibilityServiceOperations(main *gorm.DB) service.ServiceOperations {
	adapter := &compatibilityServiceAdapter{main: main}
	return service.ServiceOperations{
		AI:        adapter,
		Config:    adapter,
		Fund:      adapter,
		Group:     adapter,
		History:   adapter,
		Market:    adapter,
		Notify:    adapter,
		Recommend: adapter,
		Stock:     adapter,
	}
}

// NewProductionServices assembles compatibility-backed services for legacy
// entry points that have not yet moved behind AppRuntime.
func NewProductionServices() (service.AppServices, error) {
	return service.NewAppServicesWithDependencies(productionRuntimeDependencies().Services)
}
