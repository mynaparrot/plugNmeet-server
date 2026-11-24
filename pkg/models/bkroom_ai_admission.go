package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
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

// activeOpenPerUserAISessionMeta returns the metadata of a running open per-user AI session parent room.
func (m *BreakoutRoomModel) activeOpenPerUserAISessionMeta(parentRoomId string) (*plugnmeet.RoomMetadata, bool) {
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(parentRoomId)
	if err != nil || rInfo == nil || meta == nil || !m.natsService.IsRoomStatusActive(rInfo.GetStatus()) || !IsOpenPerUserAISessionParent(meta) {
		return nil, false
	}
	return meta, true
}

// aiSessionDurationSec returns the effective length of one AI session in seconds: the smaller of
// ai_agent_features.max_session_seconds (default 1800s) and the breakout room duration.
// It is the length of one session cycle used by waitingAISessionQueueInfos.
func (m *BreakoutRoomModel) aiSessionDurationSec(parentRoomId string, meta *plugnmeet.RoomMetadata) uint64 {
	maxSec := uint64(meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures().GetMaxSessionSeconds())
	if maxSec == 0 {
		maxSec = uint64(aiAgentDefaultMaxSessionSecond)
	}
	if bkSec := m.perUserAIBreakoutDuration(parentRoomId) * 60; bkSec > 0 && bkSec < maxSec {
		return bkSec
	}
	return maxSec
}

// waitingAISessionSlots returns the remaining seconds of each open breakout room, i.e. the
// active session slots: Created + durSec - now, clamped to at least 30s. The result is sorted
// shortest first so the earliest-ending slots are assigned first. Entries are not padded here;
// waitingAISessionQueueInfos pads the list up to maxSessions.
func (m *BreakoutRoomModel) waitingAISessionSlots(parentRoomId string, sessionDurationSec uint64) []uint64 {
	rooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(parentRoomId)
	if err != nil || len(rooms) == 0 {
		return nil
	}

	now := time.Now().Unix()
	slots := make([]uint64, 0, len(rooms))
	for _, raw := range rooms {
		room := new(plugnmeet.BreakoutRoom)
		if err = protojson.Unmarshal([]byte(raw), room); err != nil {
			continue
		}

		durSec := sessionDurationSec
		if roomSec := room.GetDuration() * 60; roomSec > 0 && roomSec < durSec {
			durSec = roomSec
		}
		endsAt := int64(room.GetCreated()) + int64(durSec)
		if room.GetCreated() == 0 {
			endsAt = now + int64(durSec)
		}
		left := endsAt - now
		if left < 30 {
			left = 30
		}
		slots = append(slots, uint64(left))
	}

	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	return slots
}

// waitingAISessionQueueInfos computes the queue position and estimated wait time of every waiting participant.
//
// Inputs:
//   - sessionSec: effective length of one AI session (aiSessionDurationSec), i.e. the smaller
//     of ai_agent_features.max_session_seconds (default 1800s) and the breakout room duration.
//   - slots: remaining seconds of the open breakout rooms (waitingAISessionSlots), shortest
//     first, padded up to maxSessions (a free slot is estimated as 30s, or sessionSec when no
//     room is open at all, since a new room has to be created).
//   - waiting: FIFO ordered, so index 0 is the participant who has waited the longest.
//
// For the i-th waiting participant (0-indexed):
//
//	slot  = i % maxSessions  // concurrent slot they will inherit
//	batch = i / maxSessions  // how many full session cycles ahead of them on that slot
//	estWaitSec = slots[i%maxSessions] + (i/maxSessions)*sessionSec
//
// Example with 2 slots, sessionSec = 30m and slots = [5m, 12m]:
//
//	#1 -> 5m
//	#2 -> 12m
//	#3 -> 5m + 30m = 35m
//	#4 -> 12m + 30m = 42m
func (m *BreakoutRoomModel) waitingAISessionQueueInfos(parentRoomId string, meta *plugnmeet.RoomMetadata, waiting []*waitingAISessionUser) map[string]*plugnmeet.WaitingQueueInfo {
	maxSessions := int(effectiveMaxAISessions(meta))
	if maxSessions < 1 {
		maxSessions = 1
	}
	sessionSec := m.aiSessionDurationSec(parentRoomId, meta)

	slots := m.waitingAISessionSlots(parentRoomId, sessionSec)
	pad := uint64(30)
	if len(slots) == 0 {
		pad = sessionSec
	}
	for len(slots) < maxSessions {
		slots = append(slots, pad)
	}
	if len(slots) > maxSessions {
		slots = slots[:maxSessions]
	}

	now := time.Now().Unix()
	infos := make(map[string]*plugnmeet.WaitingQueueInfo, len(waiting))
	for i, w := range waiting {
		estWaitSec := slots[i%maxSessions] + uint64(i/maxSessions)*sessionSec
		infos[w.user.GetUserId()] = &plugnmeet.WaitingQueueInfo{
			Position:             uint32(i + 1),
			TotalWaiting:         uint32(len(waiting)),
			EstimatedWaitSeconds: estWaitSec,
			EstimatedTurnAt:      (now + int64(estWaitSec)) * 1000,
		}
	}

	return infos
}

// WaitingAISessionQueueInfo returns the current queue position of a waiting participant.
func (m *BreakoutRoomModel) WaitingAISessionQueueInfo(parentRoomId, userId string) *plugnmeet.WaitingQueueInfo {
	meta, ok := m.activeOpenPerUserAISessionMeta(parentRoomId)
	if !ok {
		return nil
	}
	waiting := m.waitingAISessionUsers(parentRoomId, "")
	if len(waiting) == 0 {
		return nil
	}

	return m.waitingAISessionQueueInfos(parentRoomId, meta, waiting)[userId]
}

// syncWaitingAISessionQueue broadcasts the current queue position and ETA of every waiting participant.
func (m *BreakoutRoomModel) syncWaitingAISessionQueue(parentRoomId string) {
	meta, ok := m.activeOpenPerUserAISessionMeta(parentRoomId)
	if !ok {
		return
	}
	waiting := m.waitingAISessionUsers(parentRoomId, "")
	if len(waiting) == 0 {
		return
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomId": parentRoomId,
		"method": "syncWaitingAISessionQueue",
	})

	infos := m.waitingAISessionQueueInfos(parentRoomId, meta, waiting)
	for _, w := range waiting {
		userId := w.user.GetUserId()
		info := infos[userId]
		if info == nil || sameWaitingQueueInfo(w.metadata.GetWaitingQueueInfo(), info) {
			continue
		}

		w.metadata.WaitingQueueInfo = info
		if err := m.natsService.UpdateAndBroadcastUserMetadata(parentRoomId, userId, w.metadata, nil); err != nil {
			log.WithError(err).Warnln("failed to update the queue position")
			continue
		}
		// waiting users only listen to the private subject
		_ = m.natsService.BroadcastUserMetadata(parentRoomId, userId, nil, &userId)
	}
}

func sameWaitingQueueInfo(prev, next *plugnmeet.WaitingQueueInfo) bool {
	if prev == nil {
		return false
	}
	if prev.GetPosition() != next.GetPosition() || prev.GetTotalWaiting() != next.GetTotalWaiting() {
		return false
	}

	diff := prev.GetEstimatedTurnAt() - next.GetEstimatedTurnAt()
	if diff < 0 {
		diff = -diff
	}
	return diff < 5000
}

// TryAdmitWaitingAISessionUsers moves queued participants of an open per-user AI
// session into the free slots, oldest first.
func (m *BreakoutRoomModel) TryAdmitWaitingAISessionUsers(ctx context.Context, parentRoomId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": parentRoomId,
		"method": "TryAdmitWaitingAISessionUsers",
	})

	meta, ok := m.activeOpenPerUserAISessionMeta(parentRoomId)
	if !ok {
		return
	}

	m.aiAdmissionMu.Lock()
	defer m.aiAdmissionMu.Unlock()

	maxSessions := int64(effectiveMaxAISessions(meta))
	if existing, err := m.rs.CountBreakoutRooms(parentRoomId); err == nil && existing < maxSessions {
		for _, w := range m.waitingAISessionUsers(parentRoomId, "") {
			if existing >= maxSessions {
				break
			}
			if err := m.admitWaitingAISessionUser(ctx, parentRoomId, w); err != nil {
				if errors.Is(err, config.ErrAIAgentSessionsFull) {
					break
				}
				log.WithError(err).Warn("failed to admit a waiting participant")
				continue
			}
			existing++
		}
	}

	// the remaining waiting participants get their updated position and ETA
	m.syncWaitingAISessionQueue(parentRoomId)
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

	if err = m.admitWaitingAISessionUser(ctx, parentRoomId, &waitingAISessionUser{user: u, metadata: meta}); err != nil {
		return err
	}

	m.syncWaitingAISessionQueue(parentRoomId)
	return nil
}

// admittedUserMetadata prepares the metadata of a participant leaving the queue.
func admittedUserMetadata(md *plugnmeet.UserMetadata) *plugnmeet.UserMetadata {
	out := proto.Clone(md).(*plugnmeet.UserMetadata)
	out.WaitForApproval = false
	out.IsPresenter = false
	out.WaitingQueueInfo = nil
	return out
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

	userMeta := admittedUserMetadata(w.metadata)

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
