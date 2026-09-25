package notifier

import (
	"fmt"
	"time"

	"github.com/aomtest/komari-slim-server/database/clients"
	"github.com/aomtest/komari-slim-server/database/models"
	messageevent "github.com/aomtest/komari-slim-server/database/models/messageEvent"
	"github.com/aomtest/komari-slim-server/internal/config"
	"github.com/aomtest/komari-slim-server/pkg/timeutil"
	"github.com/aomtest/komari-slim-server/utils/messageSender"
	"github.com/aomtest/komari-slim-server/utils/renewal"
)

func CheckExpireScheduledWork() {
	CheckExpire()
}

func CheckExpire() {
	cfg, err := config.GetMany(map[string]any{
		config.ExpireNotificationEnabledKey:  false,
		config.ExpireNotificationLeadDaysKey: 7,
	})
	if err != nil {
		return
	}

	clients_all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return
	}

	checkTime := time.Now().UTC()

	// 过期提醒检查（仅当启用过期通知时）
	if cfg[config.ExpireNotificationEnabledKey].(bool) {
		notificationLeadDays := int(cfg[config.ExpireNotificationLeadDaysKey].(float64)) // Json unmarshal 会将数字解析为 float64

		type clientToExpireInfo struct {
			Name     string
			DaysLeft int
		}

		var clientLeadToExpire []clientToExpireInfo

		for _, client := range clients_all {
			if client.ExpiredAt == nil {
				continue
			}
			clientExpireTime := client.ExpiredAt.UTC()

			if clientExpireTime.Before(checkTime) {
				continue
			}

			notificationThreshold := checkTime.In(time.Local).AddDate(0, 0, notificationLeadDays).UTC()

			if clientExpireTime.Before(notificationThreshold) || clientExpireTime.Equal(notificationThreshold) {
				daysLeft := timeutil.SystemDateDistance(checkTime, clientExpireTime)

				clientLeadToExpire = append(clientLeadToExpire, clientToExpireInfo{
					Name:     client.Name,
					DaysLeft: daysLeft,
				})
			}
		}

		if len(clientLeadToExpire) > 0 {
			message := ""
			for _, clientInfo := range clientLeadToExpire {
				message += fmt.Sprintf("• %s (%dd)\n", clientInfo.Name, clientInfo.DaysLeft)
			}
			_ = messageSender.SendNotification(models.EventMessage{
				Event:   messageevent.Expire,
				Time:    time.Now().UTC(),
				Message: message,
				Emoji:   "⏳",
			})
		}
	}

	for _, client := range clients_all {
		renewal.CheckAndAutoRenewal(client)
	}
}
