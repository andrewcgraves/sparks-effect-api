package transit

import "context"

type Repository interface {
	CreateScenario(ctx context.Context, sc Scenario) error
	UpdateScenario(ctx context.Context, sc Scenario) error
	DeleteScenario(ctx context.Context, id string) error
	CountUnownedScenarioChildren(ctx context.Context, scenarioID string) (int, error)
	GetScenarioByID(ctx context.Context, id string) (Scenario, bool, error)
	GetScenarioBySlug(ctx context.Context, slug string) (Scenario, bool, error)
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
	CreateRoute(ctx context.Context, r Route) error
	GetRouteBySlug(ctx context.Context, slug string) (Route, bool, error)
	UpdateRoute(ctx context.Context, r Route) error
	DeleteRoute(ctx context.Context, id string) error
	ListCuratedRouteSummaries(ctx context.Context) ([]RouteSummary, error)
	ListRouteSummariesByOwner(ctx context.Context, ownerID string) ([]RouteSummary, error)
	CountRouteDependents(ctx context.Context, routeID string) (RouteDependents, error)
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]Route, error)
	ListRoutesByIDs(ctx context.Context, ids []string) ([]Route, error)
	CreateStation(ctx context.Context, st Station) error
	GetStationBySlug(ctx context.Context, scenarioID, slug string) (Station, bool, error)
	UpdateStation(ctx context.Context, st Station) error
	DeleteStation(ctx context.Context, id string) error
	CountStationDependents(ctx context.Context, stationID string) (int, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]Station, error)
	CreateVehicleType(ctx context.Context, vt VehicleType) error
	GetVehicleTypeByID(ctx context.Context, id string) (VehicleType, bool, error)
	ListVehicleTypes(ctx context.Context) ([]VehicleType, error)
	CreateService(ctx context.Context, svc Service) error
	GetServiceByID(ctx context.Context, id string) (Service, bool, error)
	UpdateService(ctx context.Context, svc Service) error
	DeleteService(ctx context.Context, id string) error
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]Service, error)
	AddServiceToScenario(ctx context.Context, scenarioID, serviceID string) error
	ListServiceIDsByScenario(ctx context.Context, scenarioID string) ([]string, error)
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]ServiceMembership, error)
	CreateUserService(ctx context.Context, svc UserService) error
	GetUserServiceByID(ctx context.Context, id string) (UserService, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (UserService, bool, error)
	ListUserServicesByOwner(ctx context.Context, ownerID string) ([]UserService, error)
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]UserService, error)
	UpdateUserService(ctx context.Context, svc UserService) error
	DeleteUserService(ctx context.Context, id string) error
	CreateUserScenario(ctx context.Context, sc UserScenario) error
	GetUserScenarioByID(ctx context.Context, id string) (UserScenario, bool, error)
	GetUserScenarioBySlug(ctx context.Context, slug string) (UserScenario, bool, error)
	ListUserScenariosByOwner(ctx context.Context, ownerID string) ([]UserScenario, error)
	UpdateUserScenario(ctx context.Context, sc UserScenario) error
	DeleteUserScenario(ctx context.Context, id string) error
	UserServiceIDsOwnedBy(ctx context.Context, ownerID string, ids []string) (map[string]bool, error)
	UpsertTravelTimes(ctx context.Context, tt TravelTimes) error
	GetTravelTimes(ctx context.Context, scenarioSlug string) (TravelTimes, bool, error)
	ListScenariosByOwner(ctx context.Context, ownerID string) ([]Scenario, error)
	ListServicesByOwner(ctx context.Context, ownerID string) ([]Service, error)
	CreateUser(ctx context.Context, u User, passwordHash string) error
	GetUserByID(ctx context.Context, id string) (User, bool, error)
	GetUserByEmail(ctx context.Context, email string) (User, bool, error)
	GetUserCredentialsByEmail(ctx context.Context, email string) (User, string, bool, error)
	ListUsers(ctx context.Context) ([]User, error)
	CreateSession(ctx context.Context, s Session) error
	GetSessionUser(ctx context.Context, tokenHash string) (User, bool, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) (int64, error)
	CreateJob(ctx context.Context, j Job) error
	GetJobByID(ctx context.Context, id string) (Job, bool, error)
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	CompleteJob(ctx context.Context, id string, result TransitGraph, compiledServiceIDs []string) error
	ListJobs(ctx context.Context) ([]Job, error)
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (Job, bool, error)
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (Job, bool, error)
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (Job, bool, error)
	CreateRoutingJob(ctx context.Context, j *RoutingJob) error
	GetRoutingJobByID(ctx context.Context, id string) (RoutingJob, bool, error)
	FailRoutingJob(ctx context.Context, id, errMsg string) error
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]PrerenderedIsochrone, error)
	GetPrerenderedIsochrone(ctx context.Context, id string) (PrerenderedIsochrone, bool, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *PrerenderedIsochrone) error
}
