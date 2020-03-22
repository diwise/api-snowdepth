package ngsi

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"github.com/iot-for-tillgenglighet/api-snowdepth/internal/pkg/ngsi-ld/errors"
	"github.com/iot-for-tillgenglighet/api-snowdepth/internal/pkg/ngsi-ld/types"
	"github.com/iot-for-tillgenglighet/api-snowdepth/pkg/models"
)

//entitiesDatastore is an interface containing all the entities related datastore functions
type entitiesDatastore interface {
	GetLatestSnowdepths() ([]models.Snowdepth, error)
	GetLatestSnowdepthsForDevice(device string) ([]models.Snowdepth, error)
}

func convertDatabaseRecordToWeatherObserved(r *models.Snowdepth) *types.WeatherObserved {
	if r != nil {
		entity := types.NewWeatherObserved(r.Device, r.Latitude, r.Longitude, r.Timestamp)
		entity.SnowHeight = types.NewNumberProperty(math.Round(float64(r.Depth*10)) / 10)
		return entity
	}

	return nil
}

//NewQueryEntitiesHandler handles GET requests for NGSI entitites
func NewQueryEntitiesHandler(db entitiesDatastore) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entityTypes := r.URL.Query().Get("type")

		if entityTypes != "" && entityTypes != "WeatherObserved" {
			errors.ReportNewInvalidRequest(
				w,
				"This service only supports the WeatherObserved entity type.",
			)
			return
		}

		attributes := strings.Split(r.URL.Query().Get("attrs"), ",")
		for _, attr := range attributes {
			if attr != "" && attr != "snowHeight" {
				errors.ReportNewInvalidRequest(
					w,
					"This service only supports the snowHeight attribute.",
				)
				return
			}
		}

		var snowdepths []models.Snowdepth
		var err error

		query := r.URL.Query().Get("q")
		refDevicePrefix := "refDevice==\""

		if strings.HasPrefix(query, refDevicePrefix) {
			splitElems := strings.Split(query, "\"")
			deviceIDPrefix := "urn:ngsi-ld:Device:"
			deviceID := splitElems[1]

			if strings.HasPrefix(deviceID, deviceIDPrefix) {
				deviceID = strings.TrimPrefix(deviceID, deviceIDPrefix)
				snowdepths, err = db.GetLatestSnowdepthsForDevice(deviceID)
			} else {
				errors.ReportNewInvalidRequest(w, "Invalid device reference: "+deviceID)
				return
			}
		} else {
			snowdepths, err = db.GetLatestSnowdepths()
		}

		if err != nil {
			errors.ReportNewInternalError(
				w,
				"An internal error was encountered when trying to get entities from the database.",
			)
			return
		}

		depthcount := len(snowdepths)
		ngsiEntities := []*types.WeatherObserved{}

		if depthcount > 0 {
			ngsiEntities = make([]*types.WeatherObserved, 0, depthcount)

			for _, v := range snowdepths {
				ngsiEntities = append(ngsiEntities, convertDatabaseRecordToWeatherObserved(&v))
			}
		}

		bytes, err := json.MarshalIndent(ngsiEntities, "", "  ")
		if err != nil {
			errors.ReportNewInternalError(w, "Failed to encode response.")
			return
		}

		w.Header().Add("Content-Type", "application/ld+json")
		w.Write(bytes)
	})
}