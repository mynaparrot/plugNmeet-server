package models

import (
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *NatsModel) HandleInitialData(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "HandleInitialData",
	})

	// send room info
	rInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil {
		log.WithError(err).Errorln("error getting room info")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if rInfo == nil {
		log.Errorln("room information not found")
		_ = m.natsService.NotifyErrorMsg(roomId, "room information not found", &userId)
		return
	}

	// send this user's info
	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil {
		log.WithError(err).Errorln("error getting user info")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return
	}
	if userInfo == nil {
		log.Errorln("user info not found")
		_ = m.natsService.NotifyErrorMsg(roomId, "no user found", &userId)
		return
	}

	initial := &plugnmeet.NatsInitialData{
		Room:      rInfo,
		LocalUser: userInfo,
	}

	// send important info first
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RES_INITIAL_DATA, roomId, initial, &userId)
	if err != nil {
		log.WithError(err).Warnln("error sending RES_INITIAL_DATA event")
	}
}

func (m *NatsModel) HandleSendUsersList(roomId, userId string, event *plugnmeet.NatsMsgServerToClientEvents) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "HandleSendUsersList",
	})

	// Default to the original event if none is provided, for backward compatibility.
	if event == nil {
		e := plugnmeet.NatsMsgServerToClientEvents_RES_JOINED_USERS_LIST
		event = &e
	}

	users, err := m.natsService.GetOnlineUsersListAsJson(roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get online users list as json")
		return
	}

	if users != nil {
		err = m.natsService.BroadcastSystemEventToRoom(*event, roomId, users, &userId)
		if err != nil {
			log.WithError(err).Warnf("error sending event %s", event.String())
		}
	}
}

func (m *NatsModel) HandleMediaServerInfo(roomId, userId string, broadcast bool) *plugnmeet.MediaServerConnInfo {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "HandleMediaServerInfo",
	})

	userInfo, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil || userInfo == nil {
		msg := "error getting user info"
		if err != nil {
			log.WithError(err).Errorln(msg)
		}
		_ = m.natsService.NotifyErrorMsg(roomId, msg, &userId)
		return nil
	}

	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:  roomId,
		Name:    userInfo.Name,
		UserId:  userInfo.UserId,
		IsAdmin: userInfo.IsAdmin,
	}

	token, err := auth.GenerateLivekitAccessToken(m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, *m.app.Client.TokenValidity, c)
	if err != nil {
		log.WithError(err).Errorln("failed to generate livekit token")
		_ = m.natsService.NotifyErrorMsg(roomId, err.Error(), &userId)
		return nil
	}

	lkHost := strings.Replace(m.app.LivekitInfo.Host, "host.docker.internal", "localhost", 1) // without this you won't be able to connect
	data := &plugnmeet.MediaServerConnInfo{
		Url:   lkHost,
		Token: token,
	}

	if broadcast {
		err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RES_MEDIA_SERVER_DATA, roomId, data, &userId)
		if err != nil {
			log.WithError(err).Warnln("error sending RES_MEDIA_SERVER_DATA event")
		}
	}

	return data
}

func (m *NatsModel) RenewPNMToken(roomId, userId, token string) {
	// to renew token, we can add graceful period for expiry time
	// because in may case because of network related issues,
	// the client was not able to renew token
	// as renew request is coming from nats, so it should be secure
	gracefulPeriod := time.Hour * 3
	token, err := m.authModel.RenewPNMToken(token, gracefulPeriod)
	if err != nil {
		m.logger.Errorln(fmt.Errorf("error renewing pnm token for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
		return
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_RENEW_PNM_TOKEN, roomId, token, &userId)
	if err != nil {
		m.logger.Errorln(fmt.Errorf("error sending RESP_RENEW_PNM_TOKEN event for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
	}
}

func (m *NatsModel) HandleClientPing(roomId, userId string) {
	// check user status
	// if we found offline/disconnected, then we'll update
	//  because the server may receive this join status a bit lately
	// as user has sent ping request, this indicates the user is online
	// OnAfterUserJoined will check the current status and act if the user was not online.
	m.OnAfterUserJoined(roomId, userId)

	lastPing := fmt.Sprintf("%d", time.Now().UnixMilli())
	err := m.natsService.UpdateUserKeyValue(roomId, userId, natsservice.UserLastPingAt, lastPing)
	if err != nil {
		m.logger.Errorln(fmt.Sprintf("error updating user last ping for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
	}

	// send pong back to the user time to make sure that both are connected
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_PONG, roomId, lastPing, &userId)
	if err != nil {
		m.logger.WithError(err).Warnln("failed to send PONG to user")
	}
}

// HandleToDeliveryPrivateData will work as a medium to deliver private messages
// it will extract the header from message and deliver binary data to the user through private channel
func (m *NatsModel) HandleToDeliveryPrivateData(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	header := new(plugnmeet.PrivateDataDelivery)
	err := protojson.Unmarshal([]byte(req.Msg), header)
	if err != nil {
		m.logger.WithError(err).Errorln("error unmarshalling private data header")
		return
	}

	err = m.natsService.BroadcastSystemEventToRoomWithBinMsg(plugnmeet.NatsMsgServerToClientEvents_DELIVERY_PRIVATE_DATA, roomId, req.Msg, req.BinMsg, &header.ToUserId)
	if err != nil {
		m.logger.WithError(err).Errorf("error sending delivery private data to user %s", header.ToUserId)
	}

	// like chat messages need to send back to sender as well
	if header.EchoToSender {
		err = m.natsService.BroadcastSystemEventToRoomWithBinMsg(plugnmeet.NatsMsgServerToClientEvents_DELIVERY_PRIVATE_DATA, roomId, req.Msg, req.BinMsg, &userId)
		if err != nil {
			m.logger.WithError(err).Errorf("error sending delivery private data to sender user %s", userId)
		}
	}
}
