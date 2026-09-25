package messageSender

import (
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/bark"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/email"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/empty"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/serverchan3"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/serverchanturbo"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/telegram"
	_ "github.com/aomtest/komari-slim-server/utils/messageSender/webhook"
)

func All() {
}
