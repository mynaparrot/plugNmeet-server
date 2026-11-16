package models

import (
	"context"
	"errors"
	"sort"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

// Redelegate forces delegate re-selection and returns the new delegate id.
func (m *AIAgentModel) Redelegate(ctx context.Context, roomId string, req *plugnmeet.AIAgentRedelegateReq) (string, error) {
	delegate, err := m.selectDelegate(ctx, roomId, req.GetTargetUserId())
	if err != nil {
		return "", err
	}
	if delegate == "" {
		return "", config.ErrAIAgentNoDelegate
	}

	return delegate, nil
}

// SelectOrGetDelegate returns the participant that executes client-delegated
// tools, keeping a still-valid assignment.
func (m *AIAgentModel) SelectOrGetDelegate(ctx context.Context, roomId string) (string, error) {
	current, err := m.rds.Get(ctx, aiAgentKey(roomId, aiAgentKeyDelegate)).Result()
	if err == nil && current != "" && m.isEligibleDelegate(roomId, current) {
		return current, nil
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}

	return m.selectDelegate(ctx, roomId, "")
}

// selectDelegate picks one online human, preferring an admin when the caller did
// not name a valid candidate.
func (m *AIAgentModel) selectDelegate(ctx context.Context, roomId, preferredUserId string) (string, error) {
	if preferredUserId != "" && m.isEligibleDelegate(roomId, preferredUserId) {
		return m.storeDelegate(ctx, roomId, preferredUserId)
	}

	humans := m.onlineHumans(roomId)
	if len(humans) == 0 {
		_ = m.rds.Del(ctx, aiAgentKey(roomId, aiAgentKeyDelegate)).Err()
		return "", nil
	}

	// A 1-on-1 room names its participants, and one of them owns the session.
	if allowed := m.roomAgentFeatures(roomId).GetAllowedParticipantIds(); len(allowed) > 0 {
		for _, user := range humans {
			if containsUserId(allowed, user.GetUserId()) {
				return m.storeDelegate(ctx, roomId, user.GetUserId())
			}
		}
	}

	target := humans[0]
	for _, user := range humans {
		if user.GetIsAdmin() {
			target = user
			break
		}
	}

	return m.storeDelegate(ctx, roomId, target.GetUserId())
}

// CanControlSession reports whether userId may drive the room's agent session:
// the current delegate, or one of the pre-authorized participants.
func (m *AIAgentModel) CanControlSession(ctx context.Context, roomId, userId string) bool {
	if m == nil || userId == "" {
		return false
	}
	if m.IsUserAgentDelegate(ctx, roomId, userId) {
		return true
	}

	return containsUserId(m.roomAgentFeatures(roomId).GetAllowedParticipantIds(), userId)
}

func (m *AIAgentModel) storeDelegate(ctx context.Context, roomId, userId string) (string, error) {
	if err := m.rds.Set(ctx, aiAgentKey(roomId, aiAgentKeyDelegate), userId, 0).Err(); err != nil {
		return "", err
	}
	return userId, nil
}

// IsUserAgentDelegate reports whether userId is the room's current delegate.
func (m *AIAgentModel) IsUserAgentDelegate(ctx context.Context, roomId, userId string) bool {
	if m == nil || userId == "" {
		return false
	}
	current, err := m.rds.Get(ctx, aiAgentKey(roomId, aiAgentKeyDelegate)).Result()
	return err == nil && current != "" && current == userId
}

func (m *AIAgentModel) isEligibleDelegate(roomId, userId string) bool {
	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	if err != nil || status != natsservice.UserStatusOnline {
		return false
	}
	if config.IsUserIdInternal(userId) || m.natsService.IsUserExistInBlockList(roomId, userId) {
		return false
	}
	// a participant who is still in the waiting room is not part of the session
	if _, metadata, _ := m.natsService.GetUserWithMetadata(roomId, userId); metadata.GetWaitForApproval() {
		return false
	}

	return true
}

// onlineHumans returns the online, eligible participants of a room, ordered by id.
func (m *AIAgentModel) onlineHumans(roomId string) []*plugnmeet.NatsKvUserInfo {
	users, err := m.natsService.GetOnlineUsersList(roomId)
	if err != nil {
		return nil
	}

	humans := make([]*plugnmeet.NatsKvUserInfo, 0, len(users))
	for _, user := range users {
		if user == nil || config.IsUserIdInternal(user.GetUserId()) {
			continue
		}
		if m.natsService.IsUserExistInBlockList(roomId, user.GetUserId()) {
			continue
		}
		if metadata, err := m.natsService.UnmarshalUserMetadata(user.GetMetadata()); err == nil && metadata.GetWaitForApproval() {
			continue
		}
		humans = append(humans, user)
	}
	sort.Slice(humans, func(i, j int) bool {
		return humans[i].GetUserId() < humans[j].GetUserId()
	})

	return humans
}

// AcquirePresenterForAgent promotes the agent to presenter and remembers the
// human presenter it replaced.
func (m *AIAgentModel) AcquirePresenterForAgent(ctx context.Context, roomId, agentUserId string) {
	previous, err := m.userModel.FindCurrentPresenter(roomId)
	if err != nil {
		m.logger.WithError(err).Warn("could not read the current presenter")
	}
	if previous != "" && previous != agentUserId {
		if err := m.rds.Set(ctx, aiAgentKey(roomId, aiAgentKeyPrevPresenter), previous, 0).Err(); err != nil {
			m.logger.WithError(err).Warn("could not remember the previous presenter")
		}
	}
	// SwitchPresenter promotes before demoting, so the room is never presenter-less.
	if _, err := m.userModel.SwitchPresenter(&plugnmeet.SwitchPresenterReq{
		RoomId: roomId,
		UserId: agentUserId,
		Task:   plugnmeet.SwitchPresenterTask_PROMOTE,
	}); err != nil {
		m.logger.WithError(err).Warn("could not promote the ai agent to presenter")
	}
}

// RestorePreviousPresenter hands the canvas back to the human presenter. A room
// is never left with a student as presenter.
func (m *AIAgentModel) RestorePreviousPresenter(ctx context.Context, roomId string) {
	key := aiAgentKey(roomId, aiAgentKeyPrevPresenter)
	previous, err := m.rds.Get(ctx, key).Result()
	_ = m.rds.Del(ctx, key).Err()
	if err != nil || previous == "" {
		return
	}

	target := ""
	if status, _ := m.natsService.GetRoomUserStatus(roomId, previous); status == natsservice.UserStatusOnline {
		target = previous
	} else {
		for _, user := range m.onlineHumans(roomId) {
			if user.GetIsAdmin() {
				target = user.GetUserId()
				break
			}
		}
	}
	if target == "" {
		return
	}
	if _, err := m.userModel.SwitchPresenter(&plugnmeet.SwitchPresenterReq{
		RoomId: roomId,
		UserId: target,
		Task:   plugnmeet.SwitchPresenterTask_PROMOTE,
	}); err != nil {
		m.logger.WithError(err).Warn("could not restore the previous presenter")
	}
}
