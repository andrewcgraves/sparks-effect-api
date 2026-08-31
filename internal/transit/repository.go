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
	UpdateScenario(ctx context.Context, sc Scenario) error
	DeleteScenario(ctx context.Context, id string) error
	// CountCuratedScenarioChildren reports curated rows living under a scenario.
	// Deleting a scenario cascades to its children, which is safe exactly while
	// the uniformity invariant holds; a non-zero count means it does not, and
	// the delete is refused rather than cascading over someone else's rows.
	CountCuratedScenarioChildren(ctx context.Context, scenarioID string) (int, error)
	// GetScenarioByID resolves a scenario the way a compile job names its
	// target; GetScenarioBySlug is how every request-facing path addresses one.
	GetScenarioByID(ctx context.Context, id string) (Scenario, bool, error)
	GetScenarioBySlug(ctx context.Context, slug string) (Scenario, bool, error)
	// ListCuratedScenarios returns only the scenarios with no owner — the
	// platform's own curated data. It is the containment boundary for owned
	// content: LoadStore compiles everything this returns into the public
	// in-memory store, so a user-authored scenario must never appear in it.
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)

	// Routes. A route is addressed globally by slug, since an admin-ingested
	// alignment belongs to no scenario; ListRoutesByScenario covers the seeded
	// routes that do. The two summary reads are the only route reads that are
	// not whole-aggregate: choosing a route needs none of its bulk.
	//
	// GetRouteBySlug is deliberately unfiltered. routes.slug is globally UNIQUE
	// across curated and owned rows, so slug minting has to be able to probe the
	// whole namespace; filtering here would hand a user a constraint violation
	// instead of the next free slug. Ownership is checked by the handler.
	CreateRoute(ctx context.Context, r Route) error
	GetRouteBySlug(ctx context.Context, slug string) (Route, bool, error)
	UpdateRoute(ctx context.Context, r Route) error
	DeleteRoute(ctx context.Context, id string) error
	// ListCuratedRouteSummaries backs the public picker: unowned routes only.
	// ListRouteSummariesByOwner is its complement, backing GET /api/me/routes.
	ListCuratedRouteSummaries(ctx context.Context) ([]RouteSummary, error)
	ListRouteSummariesByOwner(ctx context.Context, ownerID string) ([]RouteSummary, error)
	// CountRouteDependents reports what would break if this route were deleted,
	// so the API can refuse with a 409 rather than let user_services.route_id's
	// ON DELETE CASCADE silently destroy a user's saved services.
	CountRouteDependents(ctx context.Context, routeID string) (RouteDependents, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	// ListRoutesByIDs loads routes by id, the whole aggregate — how a
	// user-authored compile resolves the alignments its services run on.
	ListRoutesByIDs(ctx context.Context, ids []string) ([]Route, error)

	// Stations are addressed by (scenario, slug): stations.slug is unique per
	// scenario, not globally, so there is no station read that is not
	// scenario-scoped.
	CreateStation(ctx context.Context, st Station) error
	GetStationBySlug(ctx context.Context, scenarioID, slug string) (Station, bool, error)
	UpdateStation(ctx context.Context, st Station) error
	DeleteStation(ctx context.Context, id string) error
	// CountStationDependents reports the service stops referencing this station.
	// service_stops.station_id is RESTRICT, so without this the delete surfaces
	// as an opaque 500 instead of a 409.
	CountStationDependents(ctx context.Context, stationID string) (int, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)

	// VehicleTypes are global (not scenario-scoped); CreateVehicleType is
	// idempotent on ID so shared seed rows can be written once per scenario.
	// They carry no owner: rolling stock is a shared catalog any authored
	// service may reference.
	CreateVehicleType(ctx context.Context, vt VehicleType) error
	GetVehicleTypeByID(ctx context.Context, id string) (VehicleType, bool, error)
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)

	// Services, persisted with their embedded stops and frequency windows.
	//
	// ListServicesByScenario is deliberately unfiltered, for
	// ListStationsByScenario's reason: the uniformity invariant means scoping to
	// a scenario has already scoped to its owner.
	CreateService(ctx context.Context, svc Service) error
	GetServiceByID(ctx context.Context, id string) (Service, bool, error)
	UpdateService(ctx context.Context, svc Service) error
	DeleteService(ctx context.Context, id string) error
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)

	// ScenarioService is the curated membership join: which services a scenario
	// exposes, independent of ownership.
	AddServiceToScenario(ctx context.Context, scenarioID, serviceID string) error
	ListServiceIDsByScenario(ctx context.Context, scenarioID string) ([]string, error)
	// ListServiceMembershipByScenario is that same membership plus each
	// member's updated_at — the two facts staleness compares (MembershipStale).
	// It is a read of its own rather than a use of ListServicesByScenario
	// because Service is a domain value with no updated_at on it, and hydrating
	// every member's stops and frequency windows to reach a timestamp would be
	// most of a scenario's rows for a comparison that reads none of them.
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]ServiceMembership, error)

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

	// TravelTimes (adjacent segment run times) per scenario. Written whole:
	// a segment has no identity a client can address, so an edit replaces the
	// set rather than diffing it.
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
	// GetLatestSucceededUserServiceJob is the single-service counterpart, for a
	// service compiled alone as the degenerate one-member scenario.
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (Job, bool, error)

	// RoutingJobs are isochrones handed to the routing worker (SPA-182). Only
	// the three operations this repository performs appear here: it inserts a
	// job, polls it, and fails one whose publish the broker never confirmed.
	// Every other transition — running, succeeded, and the result itself — is
	// written by the worker in the other repository, so there is no method for
	// it to drift out of step with.
	//
	// CreateRoutingJob takes a pointer because it fills in the database-assigned
	// timestamps, which the 202 response carries back to the caller.
	CreateRoutingJob(ctx context.Context, j *RoutingJob) error
	GetRoutingJobByID(ctx context.Context, id string) (RoutingJob, bool, error)
	FailRoutingJob(ctx context.Context, id, errMsg string) error

	// PrerenderedIsochrones are curated, already-computed isochrone payloads a
	// scenario ships with (see PrerenderedIsochrone).
	//
	// The list read deliberately does not carry Result: payloads run 300-500KB
	// and a scenario's page lists a handful of them to choose between, so the
	// split between "what there is" and "one of them, in full" is a split
	// between two queries rather than a field a caller may forget to drop.
	//
	// CreatePrerenderedIsochrone takes a pointer for CreateRoutingJob's reason:
	// it fills in the database-assigned timestamps, which the 201 carries back.
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]PrerenderedIsochrone, error)
	GetPrerenderedIsochrone(ctx context.Context, id string) (PrerenderedIsochrone, bool, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *PrerenderedIsochrone) error
}
