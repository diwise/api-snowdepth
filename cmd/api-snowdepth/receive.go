package main

import (
	"encoding/json"

	"github.com/rs/zerolog"
	"github.com/streadway/amqp"

	"github.com/diwise/api-snowdepth/pkg/database"
	"github.com/diwise/messaging-golang/pkg/messaging"
	"github.com/diwise/messaging-golang/pkg/messaging/telemetry"
)

func createSnowdepthReceiver(db database.Datastore) messaging.TopicMessageHandler {
	return func(msg amqp.Delivery, logger zerolog.Logger) {

		logger.Info().Str("body", string(msg.Body)).Msg("message received from queue")

		depth := &telemetry.Snowdepth{}
		err := json.Unmarshal(msg.Body, depth)

		if err != nil {
			logger.Error().Err(err).Msg("failed to unmarshal message")
			return
		}

		// TODO: Propagate database errors, catch and log them here ...
		_, err = db.AddSnowdepthMeasurement(
			&depth.Origin.Device,
			depth.Origin.Latitude, depth.Origin.Longitude,
			float64(depth.Depth),
			depth.Timestamp,
		)

		if err != nil {
			logger.Error().Err(err).Msg("failed to add snowdepth measurement")
		}
	}
}
