package main

import (
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/diwise/api-snowdepth/pkg/database"
	"github.com/diwise/api-snowdepth/pkg/handler"
	"github.com/diwise/messaging-golang/pkg/messaging"
	"github.com/diwise/messaging-golang/pkg/messaging/telemetry"
)

func main() {

	serviceName := "api-snowdepth"

	logger := log.With().Str("service", strings.ToLower(serviceName)).Logger()

	logger.Info().Msg("starting up ...")

	config := messaging.LoadConfiguration(serviceName, logger)
	messenger, _ := messaging.Initialize(config)

	defer messenger.Close()

	db, _ := database.NewDatabaseConnection(logger)

	topicName := (&telemetry.Snowdepth{}).TopicName()
	logger.Info().Msgf("registering message handler for topic %s", topicName)
	messenger.RegisterTopicMessageHandler(topicName, createSnowdepthReceiver(db))

	logger.Info().Msg("calling CreateRouterAndStartServing")
	handler.CreateRouterAndStartServing(db, messenger, logger)
}
