package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

var webhookNotifier *webhook.WebhookNotifier

func GetWebhookNotifier(roomId, roomSid string) *webhook.WebhookNotifier {
	if webhookNotifier != nil {
		return webhookNotifier
	}
	webhookNotifier = webhook.NewWebhookNotifier(config.AppCnf.Client.ApiKey, config.AppCnf.Client.Secret, config.GetLogger())
	RegisterRoomForWebhook(roomId, roomSid)

	return webhookNotifier
}

// RegisterRoomForWebhook will check if room exist already or not
// if not then it will add
// it's important to call this method from roomStarted, otherwise new room won't be adding
func RegisterRoomForWebhook(roomId, roomSid string) {
	if webhookNotifier == nil {
		return
	}
	if webhookNotifier.RoomExist(roomId) {
		return
	}
	webhookConf := config.AppCnf.Client.WebhookConf
	if webhookConf.Enable {
		var urls []string
		if webhookConf.Url != "" {
			urls = append(urls, webhookConf.Url)
		}
		if webhookConf.EnableForPerMeeting && roomSid != "" {
			m := NewRoomModel()
			roomInfo, _ := m.GetRoomInfo("", roomSid, 0)
			if roomInfo.WebhookUrl != "" {
				urls = append(urls, roomInfo.WebhookUrl)
			}
		}
		if roomId != "" && len(urls) > 0 {
			webhookNotifier.AddToWebhookQueuedNotifier(roomId, urls)
		}
	}
}
