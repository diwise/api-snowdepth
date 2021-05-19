package main

import (
	"encoding/json"

	log "github.com/sirupsen/logrus"
	"github.com/streadway/amqp"

	"github.com/diwise/api-snowdepth/pkg/database"
	"github.com/diwise/messaging-golang/pkg/messaging"
	"github.com/diwise/messaging-golang/pkg/messaging/telemetry"
)

func createSnowdepthReceiver(db database.Datastore) messaging.TopicMessageHandler {
	return func(msg amqp.Delivery) {

		log.Info("Message received from queue: " + string(msg.Body))

		depth := &telemetry.Snowdepth{}
		err := json.Unmarshal(msg.Body, depth)

		if err != nil {
			log.Error("Failed to unmarshal message")
			return
		}

		// TODO: Propagate database errors, catch and log them here ...
		db.AddSnowdepthMeasurement(
			&depth.Origin.Device,
			depth.Origin.Latitude, depth.Origin.Longitude,
			float64(depth.Depth),
			depth.Timestamp,
		)
	}
}
