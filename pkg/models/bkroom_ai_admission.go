package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

type waitingAISessionUser struct {
	user     *plugnmeet.NatsKvUserInfo
	metadata *plugnmeet.UserMetadata
}

// perUserAIBreakoutRoomId is the fixed child room id of a participant's 1-on-1 AI session.
func perUserAIBreakoutRoomId(parentRoomId, userId string) string {
	return fmt.Sprintf(BreakoutRoomFormat, parentRoomId, userId)
}

// HasPerUserAIBreakoutRoom reports whether the participant's 1-on-1 AI session room is open.
func (m *BreakoutRoomModel) HasPerUserAIBreakoutRoom(parentRoomId, userId string) bool {
	room, err := m.rs.GetBreakoutRoom(parentRoomId, perUserAIBreakoutRoomId(parentRoomId, userId))
	return err == nil && room != ""
}

// hasWaitingAISessionUsers reports whether another participant is already queued in the parent room.
func (m *BreakoutRoomModel) hasWaitingAISessionUsers(parentRoomId, excludeUserId string) bool {
	return len(m.waitingAISessionUsers(parentRoomId, excludeUserId)) > 0
}

// waitingAISessionUsers returns the queued participants of the parent room, oldest first.
func (m *BreakoutRoomModel) waitingAISessionUsers(parentRoomId, excludeUserId string) []*waitingAISessionUser {
	users, err := m.natsService.GetOnlineUsersList(parentRoomId)
	if err != nil || len(users) == 0 {
		return nil
	}

	var waiting []*waitingAISessionUser
	for _, u := range users {
		if u.GetUserId() == excludeUserId || u.GetIsAdmin() || config.IsUserIdInternal(u.GetUserId()) {
			continue
		}
		meta, err := m.natsService.UnmarshalUserMetadata(u.GetMetadata())
		if err != nil || !meta.GetWaitForApproval() {
			continue
		}
		waiting = append(waiting, &waitingAISessionUser{user: u, metadata: meta})
	}

	sort.Slice(waiting, func(i, j int) bool {
		if waiting[i].user.GetJoinedAt() != waiting[j].user.GetJoinedAt() {
			return waiting[i].user.GetJoinedAt() < waiting[j].user.GetJoinedAt()
		}
		return waiting[i].user.GetUserId() < waiting[j].user.GetUserId()
	})

	return waiting
}

// TryAdmitWaitingAISessionUsers moves queued participants of an open per-user AI
// session into the free slots, oldest first.
func (m *BreakoutRoomModel) TryAdmitWaitingAISessionUsers(ctx context.Context, parentRoomId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": parentRoomId,
		"method": "TryAdmitWaitingAISessionUsers",
	})

	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(parentRoomId)
	if err != nil || rInfo == nil || meta == nil || !m.natsService.IsRoomStatusActive(rInfo.GetStatus()) || !IsOpenPerUserAISessionParent(meta) {
		return
	}

	m.aiAdmissionMu.Lock()
	defer m.aiAdmissionMu.Unlock()

	maxSessions := int64(effectiveMaxAISessions(meta))
	existing, err := m.rs.CountBreakoutRooms(parentRoomId)
	if err != nil || existing >= maxSessions {
		return
	}

	for _, w := range m.waitingAISessionUsers(parentRoomId, "") {
		if existing >= maxSessions {
			return
		}
		if err := m.admitWaitingAISessionUser(ctx, parentRoomId, w); err != nil {
			if errors.Is(err, config.ErrAIAgentSessionsFull) {
				return
			}
			log.WithError(err).Warn("failed to admit a waiting participant")
			continue
		}
		existing++
	}
}

// AdmitWaitingAISessionUser admits one queued participant, e.g. one the host approved.
func (m *BreakoutRoomModel) AdmitWaitingAISessionUser(ctx context.Context, parentRoomId, userId string) error {
	m.aiAdmissionMu.Lock()
	defer m.aiAdmissionMu.Unlock()

	u, err := m.natsService.GetUserInfo(parentRoomId, userId)
	if err != nil {
		return err
	}
	if u == nil {
		return config.ErrRoomUserNotFound
	}
	meta, err := m.natsService.UnmarshalUserMetadata(u.GetMetadata())
	if err != nil {
		return err
	}
	if u.GetIsAdmin() || config.IsUserIdInternal(userId) || !meta.GetWaitForApproval() {
		return nil
	}

	return m.admitWaitingAISessionUser(ctx, parentRoomId, &waitingAISessionUser{user: u, metadata: meta})
}

func (m *BreakoutRoomModel) admitWaitingAISessionUser(ctx context.Context, parentRoomId string, w *waitingAISessionUser) error {
	userId := w.user.GetUserId()
	log := m.logger.WithFields(logrus.Fields{
		"roomId": parentRoomId,
		"userId": userId,
		"method": "admitWaitingAISessionUser",
	})

	childRoomId, err := m.EnsurePerUserAIBreakoutRoom(ctx, parentRoomId, userId, w.user.GetName(), w.metadata.GetPreviousSessionContext())
	if err != nil {
		return err
	}

	userMeta := proto.Clone(w.metadata).(*plugnmeet.UserMetadata)
	userMeta.WaitForApproval = false
	userMeta.IsPresenter = false

	_, token, err := m.um.GetPNMJoinToken(ctx, &plugnmeet.GenerateTokenReq{
		RoomId: childRoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:       w.user.GetUserId(),
			Name:         w.user.GetName(),
			ClientType:   w.user.GetClientType(),
			UserMetadata: userMeta,
		},
	}, false)
	if err != nil {
		log.WithError(err).Error("failed to generate the join token of the AI session room")
		return err
	}

	if _, err = m.natsService.UpdateUserMetadata(parentRoomId, userId, userMeta); err != nil {
		log.WithError(err).Warn("failed to clear the waiting flag of the admitted participant")
	}

	payload, err := json.Marshal(map[string]string{
		"token":        token,
		"targetRoomId": childRoomId,
		"title":        fmt.Sprintf("%s (AI Session)", w.user.GetName()),
	})
	if err != nil {
		return err
	}
	if err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_BREAKOUT_ROOM_USER_MOVED, parentRoomId, string(payload), &userId); err != nil {
		log.WithError(err).Warn("failed to push BREAKOUT_ROOM_USER_MOVED")
	}

	log.Info("admitted the waiting participant into their AI session room")
	return nil
}
