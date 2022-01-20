package handler

import (
	"compress/flate"
	"errors"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	gql "github.com/diwise/api-snowdepth/internal/pkg/graphql"
	"github.com/diwise/api-snowdepth/pkg/database"
	"github.com/diwise/api-snowdepth/pkg/models"
	"github.com/diwise/messaging-golang/pkg/messaging"
	"github.com/diwise/ngsi-ld-golang/pkg/datamodels/diwise"
	"github.com/diwise/ngsi-ld-golang/pkg/datamodels/fiware"
	ngsi "github.com/diwise/ngsi-ld-golang/pkg/ngsi-ld"
	ngsierrors "github.com/diwise/ngsi-ld-golang/pkg/ngsi-ld/errors"
	"github.com/diwise/ngsi-ld-golang/pkg/ngsi-ld/types"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog"
	"github.com/rs/cors"
	"github.com/rs/zerolog"
)

//RequestRouter wraps the concrete router implementation
type RequestRouter struct {
	impl *chi.Mux
}

func (router *RequestRouter) addGraphQLHandlers(db database.Datastore) {
	gqlServer := handler.New(gql.NewExecutableSchema(gql.Config{Resolvers: &gql.Resolver{}}))
	gqlServer.AddTransport(&transport.POST{})
	gqlServer.Use(extension.Introspection{})

	// TODO: Investigate some way to use closures instead of context even for GraphQL handlers
	router.impl.Use(database.Middleware(db))

	router.impl.Handle("/api/graphql/playground", playground.Handler("GraphQL playground", "/api/graphql"))
	router.impl.Handle("/api/graphql", gqlServer)
}

func (router *RequestRouter) addNGSIHandlers(contextRegistry ngsi.ContextRegistry, mq messaging.Context, logger zerolog.Logger) {
	router.Get("/ngsi-ld/v1/entities", ngsi.NewQueryEntitiesHandler(contextRegistry))
	router.Get("/ngsi-ld/v1/entities/{entity}", ngsi.NewRetrieveEntityHandler(contextRegistry))
	router.Post(
		"/ngsi-ld/v1/entities",
		ngsi.NewCreateEntityHandlerWithCallback(
			contextRegistry,
			logger,
			func(entityType, entityID string, request ngsi.Request, sublog zerolog.Logger) {
				// Read the body from the POST request
				body, _ := ioutil.ReadAll(request.BodyReader())
				// Create and send an entity created message
				ecm := &entityCreatedMessage{
					EntityType: entityType,
					EntityID:   entityID,
					Body:       string(body),
				}

				err := mq.PublishOnTopic(ecm)

				sublog = sublog.With().Str("topic", ecm.TopicName()).Logger()

				if err != nil {
					sublog.Error().Err(err).Msg("failed to post an entity created message")
					return
				}

				sublog.Info().Str("body", ecm.Body).Msg("posted an entity created event")
			}))

	router.Patch(
		"/ngsi-ld/v1/entities/{entity}/attrs/",
		ngsi.NewUpdateEntityAttributesHandlerWithCallback(
			contextRegistry,
			logger,
			func(entityType, entityID string, request ngsi.Request, sublog zerolog.Logger) {
				// Read the body from the PATCH request
				body, _ := ioutil.ReadAll(request.BodyReader())
				// Create and send an entity updated message
				eum := &entityUpdatedMessage{
					EntityType: entityType,
					EntityID:   entityID,
					Body:       string(body),
				}

				err := mq.PublishOnTopic(eum)

				sublog = sublog.With().Str("topic", eum.TopicName()).Logger()

				if err != nil {
					sublog.Error().Err(err).Msg("failed to post an entity updated message")
					return
				}

				sublog.Info().Str("body", eum.Body).Msg("posted an entity updated event")
			}))
}

func (router *RequestRouter) addProbeHandlers() {
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

//Get accepts a pattern that should be routed to the handlerFn on a GET request
func (router *RequestRouter) Get(pattern string, handlerFn http.HandlerFunc) {
	router.impl.Get(pattern, handlerFn)
}

//Patch accepts a pattern that should be routed to the handlerFn on a PATCH request
func (router *RequestRouter) Patch(pattern string, handlerFn http.HandlerFunc) {
	router.impl.Patch(pattern, handlerFn)
}

//Post accepts a pattern that should be routed to the handlerFn on a POST request
func (router *RequestRouter) Post(pattern string, handlerFn http.HandlerFunc) {
	router.impl.Post(pattern, handlerFn)
}

//newRequestRouter creates and returns a new router wrapper
func newRequestRouter() *RequestRouter {
	router := &RequestRouter{impl: chi.NewRouter()}

	router.impl.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		Debug:            false,
	}).Handler)

	// Enable gzip compression for ngsi-ld responses
	compressor := middleware.NewCompressor(flate.DefaultCompression, "application/json", "application/ld+json", "application/geo+json")
	router.impl.Use(newApiKeyMiddleware().Handler)
	router.impl.Use(compressor.Handler)

	logger := httplog.NewLogger("api-snowdepth", httplog.Options{
		JSON: true,
	})
	router.impl.Use(httplog.RequestLogger(logger))

	return router
}

func createRequestRouter(contextRegistry ngsi.ContextRegistry, db database.Datastore, mq messaging.Context, logger zerolog.Logger) *RequestRouter {
	router := newRequestRouter()

	router.addGraphQLHandlers(db)
	router.addNGSIHandlers(contextRegistry, mq, logger)
	router.addProbeHandlers()

	return router
}

//CreateRouterAndStartServing creates a request router, registers all handlers and starts serving requests
func CreateRouterAndStartServing(db database.Datastore, mq messaging.Context, logger zerolog.Logger) {

	contextRegistry := ngsi.NewContextRegistry()
	ctxSource := contextSource{db: db}
	contextRegistry.Register(ctxSource)

	remoteURL := os.Getenv("NGSI_CTX_SRC_POINTOFINTEREST")
	regex := "^urn:ngsi-ld:Beach:.+"
	registration, _ := ngsi.NewCsourceRegistration(fiware.BeachTypeName, []string{}, remoteURL, &regex)
	contextSource, _ := ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	regex = "^urn:ngsi-ld:ExerciseTrail:.+"
	registration, _ = ngsi.NewCsourceRegistration(diwise.ExerciseTrailTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_PROBLEMREPORT")
	registration, _ = ngsi.NewCsourceRegistration(fiware.Open311ServiceRequestTypeName, []string{"service_code"}, remoteURL, nil)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_TEMPERATURE")
	registration, _ = ngsi.NewCsourceRegistration(fiware.WeatherObservedTypeName, []string{"temperature"}, remoteURL, nil)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	registration, _ = ngsi.NewCsourceRegistration(fiware.WaterQualityObservedTypeName, []string{"temperature"}, remoteURL, nil)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_TRANSPORTATION")
	regex = "^urn:ngsi-ld:Road:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.RoadTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	regex = "^urn:ngsi-ld:RoadSegment:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.RoadSegmentTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	regex = "^urn:ngsi-ld:RoadSurfaceObserved:.+"
	registration, _ = ngsi.NewCsourceRegistration(diwise.RoadSurfaceObservedTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	regex = "^urn:ngsi-ld:TrafficFlowObserved:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.TrafficFlowObservedTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_DEVICES")
	regex = "^urn:ngsi-ld:Device:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.DeviceTypeName, []string{"value"}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	regex = "^urn:ngsi-ld:DeviceModel:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.DeviceModelTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_SMARTWATER")
	regex = "^urn:ngsi-ld:WaterConsumptionObserved:.+"
	registration, _ = ngsi.NewCsourceRegistration(
		fiware.WaterConsumptionObservedTypeName,
		[]string{}, remoteURL, &regex,
	)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	remoteURL = os.Getenv("NGSI_CTX_SRC_ENVIRONMENT")
	regex = "^urn:ngsi-ld:AirQualityObserved:.+"
	registration, _ = ngsi.NewCsourceRegistration(fiware.AirQualityObservedTypeName, []string{}, remoteURL, &regex)
	contextSource, _ = ngsi.NewRemoteContextSource(registration)
	contextRegistry.Register(contextSource)

	router := createRequestRouter(contextRegistry, db, mq, logger)

	port := os.Getenv("SNOWDEPTH_API_PORT")
	if port == "" {
		port = "8880"
	}

	logger.Info().Str("port", port).Msg("listening for incoming connections")

	err := http.ListenAndServe(":"+port, router.impl)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to start listening on port")
	}
}

type contextSource struct {
	db database.Datastore
}

func convertDatabaseRecordToWeatherObserved(r *models.Snowdepth) *fiware.WeatherObserved {
	if r != nil {
		entity := fiware.NewWeatherObserved("snowHeight:"+r.Device, r.Latitude, r.Longitude, r.Timestamp)
		entity.SnowHeight = types.NewNumberProperty(math.Round(float64(r.Depth*10)) / 10)
		return entity
	}

	return nil
}

func (cs contextSource) CreateEntity(typeName, entityID string, req ngsi.Request) error {
	return nil
}

func (cs contextSource) GetEntities(query ngsi.Query, callback ngsi.QueryEntitiesCallback) error {

	var snowdepths []models.Snowdepth
	var err error

	if query.HasDeviceReference() {
		deviceID := strings.TrimPrefix(query.Device(), fiware.DeviceIDPrefix)
		snowdepths, err = cs.db.GetLatestSnowdepthsForDevice(deviceID)
	} else {
		snowdepths, err = cs.db.GetLatestSnowdepths()
	}

	if err == nil {
		for _, v := range snowdepths {
			err = callback(convertDatabaseRecordToWeatherObserved(&v))
			if err != nil {
				break
			}
		}
	}

	return err
}

func (cs contextSource) GetProvidedTypeFromID(entityID string) (string, error) {
	return "", errors.New("not implemented")
}

func (cs contextSource) ProvidesAttribute(attributeName string) bool {
	return attributeName == "snowHeight"
}

func (cs contextSource) ProvidesEntitiesWithMatchingID(entityID string) bool {
	// not supported yet
	return false
}

func (cs contextSource) ProvidesType(typeName string) bool {
	return typeName == "WeatherObserved"
}

func (cs contextSource) UpdateEntityAttributes(entityID string, req ngsi.Request) error {
	return errors.New("UpdateEntityAttributes is not supported")
}

func (cs contextSource) RetrieveEntity(entityID string, req ngsi.Request) (ngsi.Entity, error) {
	return nil, errors.New("RetrieveEntity is not supported")
}

type ApiKey struct {
	enabled bool
	key     string
}

func newApiKeyMiddleware() *ApiKey {
	a := &ApiKey{
		enabled: false,
		key:     "",
	}

	if b, err := strconv.ParseBool(os.Getenv("DIWISE_REQUIRE_API_KEY")); err == nil {
		if b {

			a.enabled = true
			validKey := os.Getenv("DIWISE_API_KEY")

			if len(validKey) != 0 {
				a.key = validKey
			} else {
				panic("Api-Key is missing or invalid. Ensure that DIWISE_API_KEY is set to a valid value")
			}
		}
	}

	return a
}

func (a *ApiKey) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.enabled && strings.ToUpper(r.Method) == "POST" {

			apiKey := r.Header.Get("x-api-key")

			if len(apiKey) == 0 || apiKey != a.key {
				ngsierrors.ReportUnauthorizedRequest(w, "Access denied. Invalid api-key found.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// TODO: Move these message types to a public messaging package that can be used by consumers

type entityCreatedMessage struct {
	EntityType string `json:"type"`
	EntityID   string `json:"id"`
	Body       string `json:"body"`
}

func (ecm *entityCreatedMessage) ContentType() string {
	return "application/json"
}

func (ecm *entityCreatedMessage) TopicName() string {
	return "ngsi-entity-created"
}

type entityUpdatedMessage struct {
	EntityType string `json:"type"`
	EntityID   string `json:"id"`
	Body       string `json:"body"`
}

func (eum *entityUpdatedMessage) ContentType() string {
	return "application/json"
}

func (eum *entityUpdatedMessage) TopicName() string {
	return "ngsi-entity-updated"
}
