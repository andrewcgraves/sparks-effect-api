package transit

import "context"

// Repository is the storage-agnostic seam for reading and writing transit domain
// data. It exists for testability, not engine-swapping: the concrete
// implementation (Postgres) is free to use native database types. A Store is
// built from a Repository via LoadStore, which reads rows and compiles the
// in-memory TransitGraph.
//
// Writes are per-aggregate: CreateService persists the service together with its
// embedded stops and frequency windows in one call. Reads hydrate the same
// aggregate shape the compiler and handlers expect.
type Repository interface {
	// Scenarios.
	CreateScenario(ctx context.Context, sc Scenario) error
	GetScenarioBySlug(ctx context.Context, slug string) (Scenario, bool, error)
	ListScenarios(ctx context.Context) ([]Scenario, error)

	// Routes.
	CreateRoute(ctx context.Context, r Route) error
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)

	// Stations.
	CreateStation(ctx context.Context, st Station) error
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)

	// VehicleTypes are global (not scenario-scoped); CreateVehicleType is
	// idempotent on ID so shared seed rows can be written once per scenario.
	CreateVehicleType(ctx context.Context, vt VehicleType) error
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)

	// Services, persisted with their embedded stops and frequency windows.
	CreateService(ctx context.Context, svc Service) error
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)

	// ScenarioService is the curated membership join: which services a scenario
	// exposes, independent of ownership.
	AddServiceToScenario(ctx context.Context, scenarioID, serviceID string) error
	ListServiceIDsByScenario(ctx context.Context, scenarioID string) ([]string, error)

	// TravelTimes (adjacent segment run times) per scenario.
	UpsertTravelTimes(ctx context.Context, tt TravelTimes) error
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)

	// Users.
	CreateUser(ctx context.Context, u User) error
	GetUserByID(ctx context.Context, id string) (User, bool, error)
	GetUserByEmail(ctx context.Context, email string) (User, bool, error)
	ListUsers(ctx context.Context) ([]User, error)

	// Jobs.
	CreateJob(ctx context.Context, j Job) error
	GetJobByID(ctx context.Context, id string) (Job, bool, error)
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	ListJobs(ctx context.Context) ([]Job, error)
}
