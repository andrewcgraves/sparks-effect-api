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

	// Routes. A route is addressed globally by slug, since an admin-ingested
	// alignment belongs to no scenario; ListRoutesByScenario covers the seeded
	// routes that do. ListRouteSummaries spans both, and is the only route read
	// that is not whole-aggregate: choosing a route needs none of its bulk.
	CreateRoute(ctx context.Context, r Route) error
	GetRouteBySlug(ctx context.Context, slug string) (Route, bool, error)
	ListRouteSummaries(ctx context.Context) ([]RouteSummary, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	// ListRoutesByIDs loads routes by id, the whole aggregate — how a
	// user-authored compile resolves the alignments its services run on.
	ListRoutesByIDs(ctx context.Context, ids []string) ([]Route, error)

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

	// UserServices are user-authored services: self-contained aggregates with
	// embedded stops and inline vehicle params, written and read whole.
	// Ownership is enforced above this layer; these methods do not filter by
	// caller, so handlers must check UserService.OwnerID before mutating.
	CreateUserService(ctx context.Context, svc UserService) error
	GetUserServiceByID(ctx context.Context, id string) (UserService, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (UserService, bool, error)
	ListUserServicesByOwner(ctx context.Context, ownerID string) ([]UserService, error)
	// ListUserServicesByIDs loads the members of a user scenario for a compile.
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]UserService, error)
	UpdateUserService(ctx context.Context, svc UserService) error
	DeleteUserService(ctx context.Context, id string) error

	// UserScenarios are owner-scoped curated sets of UserService IDs. Like
	// UserServices, ownership is enforced above this layer: these methods do
	// not filter by caller, so handlers must check UserScenario.OwnerID before
	// mutating.
	CreateUserScenario(ctx context.Context, sc UserScenario) error
	GetUserScenarioByID(ctx context.Context, id string) (UserScenario, bool, error)
	GetUserScenarioBySlug(ctx context.Context, slug string) (UserScenario, bool, error)
	ListUserScenariosByOwner(ctx context.Context, ownerID string) ([]UserScenario, error)
	UpdateUserScenario(ctx context.Context, sc UserScenario) error
	DeleteUserScenario(ctx context.Context, id string) error
	// UserServiceIDsOwnedBy reports which of ids are both known user_services
	// and owned by ownerID, so scenario membership can be validated in one
	// round trip rather than one GetUserServiceByID call per id.
	UserServiceIDsOwnedBy(ctx context.Context, ownerID string, ids []string) (map[string]bool, error)

	// TravelTimes (adjacent segment run times) per scenario.
	UpsertTravelTimes(ctx context.Context, tt TravelTimes) error
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)

	// Owner-scoped reads back "my services / my scenarios". Ownership is
	// resolved in SQL rather than by filtering in the handler, so a row the
	// caller does not own is never loaded in the first place.
	ListScenariosByOwner(ctx context.Context, ownerID string) ([]Scenario, error)
	ListServicesByOwner(ctx context.Context, ownerID string) ([]Service, error)

	// Users. Accounts are admin-provisioned; passwordHash is the bcrypt hash
	// from the auth package (empty means the account cannot authenticate).
	CreateUser(ctx context.Context, u User, passwordHash string) error
	GetUserByID(ctx context.Context, id string) (User, bool, error)
	GetUserByEmail(ctx context.Context, email string) (User, bool, error)
	// GetUserCredentialsByEmail is the login path's only reader of the password
	// hash, keeping the credential off the User struct everywhere else.
	GetUserCredentialsByEmail(ctx context.Context, email string) (User, string, bool, error)
	ListUsers(ctx context.Context) ([]User, error)

	// Sessions are addressed by token hash; the bearer token is never stored.
	CreateSession(ctx context.Context, s Session) error
	// GetSessionUser resolves a token hash to its user, reporting ok=false when
	// the session is unknown, revoked, or expired — expiry is enforced here in
	// SQL so no caller can forget to check it.
	GetSessionUser(ctx context.Context, tokenHash string) (User, bool, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	// DeleteExpiredSessions prunes lapsed rows; returns the number removed.
	DeleteExpiredSessions(ctx context.Context) (int64, error)

	// Jobs.
	CreateJob(ctx context.Context, j Job) error
	GetJobByID(ctx context.Context, id string) (Job, bool, error)
	// UpdateJobStatus transitions a job to running or failed. Success goes
	// through CompleteJob instead, since it also carries the result.
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	// CompleteJob marks a job succeeded and stores its compiled result together
	// with the member service ids it compiled (see Job.CompiledServiceIDs).
	CompleteJob(ctx context.Context, id string, result TransitGraph, compiledServiceIDs []string) error
	ListJobs(ctx context.Context) ([]Job, error)
	// GetLatestSucceededJob finds the most recent succeeded job of kind for the
	// seeded scenario addressed by slug — the "result, retrievable by slug" path,
	// so a caller never needs the job id once compilation has finished.
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (Job, bool, error)
	// GetLatestSucceededUserScenarioJob is the user-authored counterpart: the
	// compiled graph retrievable by a user scenario's slug.
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (Job, bool, error)
}
