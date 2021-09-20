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

	messenger.RegisterTopicMessageHandler((&telemetry.Snowdepth{}).TopicName(), createSnowdepthReceiver(db))

	handler.CreateRouterAndStartServing(db, messenger, logger)
}
