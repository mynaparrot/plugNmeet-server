package models

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// OnUserJoined runs after a human joined: it keeps the delegate valid, cancels a
// pending empty-room teardown and auto-starts the agent when configured.
func (m *AIAgentModel) OnUserJoined(ctx context.Context, roomId, userId string) {
	if m == nil || config.IsUserIdInternal(userId) {
		return
	}
	// only actual 1-on-1 session rooms auto-start the agent; the parent lobby of
	// per-user sessions and plain group rooms are ignored
	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil || !isOneOnOneAIAgentRoom(meta) {
		return
	}
	features := NormalizeAIAgentFeatures(meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures())
	log := m.logger.WithFields(logrus.Fields{"roomId": roomId, "userId": userId, "method": "OnUserJoined"})

	// a waiting participant is not part of the session yet
	if _, metadata, _ := m.natsService.GetUserWithMetadata(roomId, userId); metadata.GetWaitForApproval() {
		log.Debug("user is waiting for approval, not joining the ai session")
		return
	}

	// Somebody is back: an empty-room teardown is no longer valid on any node.
	m.cancelClusterStopTimer(roomId)

	if _, err := m.SelectOrGetDelegate(ctx, roomId); err != nil {
		log.WithError(err).Warn("could not select a delegate")
	}
	// A per-user session may start before the learner joins, so a running agent
	// takes the identity of the participant it talks to. Anyone else (a
	// supervisor observing) is ignored.
	agent, hosted := m.hostedAgent(roomId)
	active := hosted || m.IsAgentActive(ctx, roomId)
	if active && m.CanControlSession(ctx, roomId, userId) {
		if hosted {
			agent.SetLearner(aiAgentLearnerIdentity(m.aiAgentParticipantInfo(roomId, userId)))
		} else {
			// the session runs on another node, which has to take the identity
			if err := m.publishClusterTask(&aiAgentClusterTaskPayload{
				Task:   aiAgentClusterTaskSetLearner,
				RoomId: roomId,
				UserId: userId,
			}); err != nil {
				log.WithError(err).Warn("could not relay the learner identity")
			}
		}
	}

	if !aiAgentAutoStart(features) {
		return
	}
	// in a 1-on-1 room only the named participant starts the session, so an
	// admin observing the room waits for them
	if allowed := aiAgentAllowedParticipantIds(features); len(allowed) > 0 && !containsUserId(allowed, userId) {
		return
	}
	// the session already runs, here or on the node hosting it
	if active {
		return
	}
	if err := m.Start(ctx, roomId, nil); err != nil {
		log.WithError(err).Warn("auto-start of the ai agent failed")
	}
}

// OnUserLeft runs after a human really left: it re-selects the delegate and ends
// the session of an empty room. An intentional leave ends it at once, a dropped
// connection or refresh only after the grace period.
func (m *AIAgentModel) OnUserLeft(ctx context.Context, roomId, userId string, intentional ...bool) {
	if m == nil || config.IsUserIdInternal(userId) {
		return
	}
	if m.roomAgentFeatures(roomId) == nil {
		return
	}
	log := m.logger.WithFields(logrus.Fields{"roomId": roomId, "userId": userId, "method": "OnUserLeft"})

	if m.IsUserAgentDelegate(ctx, roomId, userId) {
		if _, err := m.selectDelegate(ctx, roomId, ""); err != nil {
			log.WithError(err).Warn("could not re-select a delegate")
		}
	}
	if len(m.onlineHumans(roomId)) > 0 {
		return
	}
	m.onRoomEmpty(roomId, len(intentional) > 0 && intentional[0])
}

// onRoomEmpty reacts to a room without humans: a participant who left on purpose
// will not come back, so the session is closed at once and its slot is free for
// the next user. Otherwise the grace period keeps a refresh alive.
func (m *AIAgentModel) onRoomEmpty(roomId string, intentional bool) {
	if intentional {
		m.stopEmptyRoom(roomId)
		return
	}
	m.startStopTimer(roomId)
}

// stopEmptyRoom stops the hosted agent of an empty room and closes its 1-on-1
// breakout room.
func (m *AIAgentModel) stopEmptyRoom(roomId string) {
	m.cancelStopTimer(roomId)

	if agent, ok := m.hostedAgent(roomId); ok {
		agent.Stop(aiAgentStopReasonEmptyRoom)
		return
	}
	// The agent runs on another node, which stops it and closes the room after
	// the stop; closing the room here would cut the session short.
	if m.IsAgentActive(m.ctx, roomId) {
		if err := m.publishClusterTask(&aiAgentClusterTaskPayload{
			Task:        aiAgentClusterTaskStop,
			RoomId:      roomId,
			Reason:      aiAgentStopReasonEmptyRoom,
			CancelTimer: true,
		}); err != nil {
			m.logger.WithError(err).WithField("roomId", roomId).Warn("could not relay the empty-room stop")
		}
		return
	}
	// No agent runs anywhere (already stopped or never started), so nothing
	// triggers afterAgentStopped: the room has to be closed directly.
	m.closeBreakoutRoomAfterStop(roomId, aiAgentStopReasonEmptyRoom)
}

// StopForRoomEnd stops the agent because the room is ending. The Redis keys
// stay, since the session summary is built from them right after.
func (m *AIAgentModel) StopForRoomEnd(ctx context.Context, roomId string) {
	if m == nil {
		return
	}
	if err := m.Stop(ctx, roomId, "room_ended"); err != nil {
		m.logger.WithError(err).Error("failed to stop the ai agent")
	}
	// An empty-room grace timer may still be pending on the node where the last participant disconnected,
	// so it has to be dropped cluster-wide.
	m.cancelClusterStopTimer(roomId)
}

// cleanupRoomState removes every room-scoped key of the agent, including the
// host-supplied provider credentials, the external MCP configuration, the
// pre-warmed tools, the output cache and the knowledge cache. The keys also
// carry a TTL as a safety net.
func (m *AIAgentModel) cleanupRoomState(ctx context.Context, roomId string) {
	_ = m.rds.Del(ctx,
		aiAgentKey(roomId, aiAgentKeyState),
		aiAgentKey(roomId, aiAgentKeyDelegate),
		aiAgentKey(roomId, aiAgentKeyPrevPresenter),
	).Err()
	// The room's insights live cost key is NOT removed here: the session can
	// still produce usage (transcription, translation, text chat) until the room
	// ends, and CreateAllRoomUsageArtifacts cleans the key up once every usage
	// artifact has been harvested at the end of onAfterRoomEnded.
	m.redisService.CleanupRoomExternalMCP(ctx, roomId)
	// the room's own provider credentials are the most sensitive key of the
	// room, so they are removed with the rest of its state
	if err := m.redisService.DeleteAIAgentCustomProvider(ctx, roomId); err != nil {
		m.logger.WithError(err).WithField("roomId", roomId).Debug("could not delete the custom ai provider configuration")
	}
}

// OnPollResponseSubmitted pushes a submitted poll or quiz answer into the live
// session so the agent can react immediately instead of polling.
func (m *AIAgentModel) OnPollResponseSubmitted(ctx context.Context, roomId, pollId, userId, userName string, selectedOption int) {
	if m == nil || config.IsUserIdInternal(userId) {
		return
	}
	agent, hosted := m.hostedAgent(roomId)
	if !hosted && !m.IsAgentActive(ctx, roomId) {
		return
	}

	noteCtx, cancel := context.WithTimeout(ctx, aiAgentPollContextBudget)
	defer cancel()

	note := m.buildPollNote(noteCtx, roomId, pollId, userId, userName, selectedOption)
	if note == "" {
		return
	}
	// proactive: the answer alone must make the agent speak up
	if hosted {
		if err := agent.SendProactiveContext(note, string(aiAgentContextSourcePoll)); err != nil {
			m.logger.WithError(err).Debug("could not send the poll context to the ai agent")
		}
		return
	}
	if err := m.publishClusterTask(&aiAgentClusterTaskPayload{
		Task:      aiAgentClusterTaskSendContext,
		RoomId:    roomId,
		Text:      note,
		Source:    aiAgentContextSourcePoll,
		Proactive: true,
	}); err != nil {
		m.logger.WithError(err).Debug("could not relay the poll context to the hosting node")
	}
}

const wrongQuizAnswerFollowUp = "Do not drop this concept or move on yet: explain clearly why their answer is wrong and why the correct option works using a concrete example, shared notepad notes, or a whiteboard drawing before asking the next progressive question."

// pollNoteOnce is appended to every poll note: the note is delivered again with
// the next turn's history, so reacting to it twice is the default failure.
const pollNoteOnce = "React to this submitted answer only once: never repeat this acknowledgement on a later turn."

// buildPollNote renders the [SYSTEM CONTEXT] note for a submitted answer. It
// returns "" when the answer cannot be described (e.g. anonymous polls).
func (m *AIAgentModel) buildPollNote(ctx context.Context, roomId, pollId, userId, userName string, selectedOption int) string {
	info, err := m.redisService.GetPollInfoByPollId(roomId, pollId)
	if err != nil || info == nil {
		return ""
	}
	respondent, err := m.redisService.GetPollRespondent(roomId, pollId, userId)
	if err != nil {
		return ""
	}

	who := sanitizePollField(firstNonEmptyString(userName, respondent.GetName(), "the participant"))
	question := sanitizePollField(info.GetQuestion())

	if info.GetIsTextAnswer() {
		answer := truncate(sanitizePollField(respondent.GetTextAnswer()), 300)
		if answer == "" {
			return ""
		}
		return fmt.Sprintf("%s answered the question %q with: %q. React in one short sentence and continue the topic. %s",
			who, question, answer, pollNoteOnce)
	}

	optionIds := respondent.GetOptionIds()
	if len(optionIds) == 0 && selectedOption > 0 {
		// Anonymous polls store no option ids; the caller's report is enough.
		optionIds = []uint64{uint64(selectedOption)}
	}

	answerIndex, answerText := -1, ""
	for _, id := range optionIds {
		for i, option := range info.GetOptions() {
			if option.GetId() == uint32(id) {
				answerIndex, answerText = i, sanitizePollField(option.GetText())
				break
			}
		}
		if answerIndex >= 0 {
			break
		}
	}
	if answerIndex < 0 {
		return ""
	}

	verdict := ""
	// A wrong quiz answer needs remediation before the next question, not a
	// one-sentence acknowledgement that brushes the mistake aside.
	followUp := "React in one short sentence and continue the topic."
	if m.redisService != nil {
		if correct, explanation, found, err := m.redisService.GetAIQuizMetadata(ctx, roomId, pollId); err == nil && found {
			if correct == answerIndex {
				verdict = "That is the correct answer."
			} else if options := info.GetOptions(); correct >= 0 && correct < len(options) {
				verdict = fmt.Sprintf("That is not the correct answer; option %d (%q) is correct.", correct+1, sanitizePollField(options[correct].GetText()))
				followUp = wrongQuizAnswerFollowUp
			} else {
				verdict = fmt.Sprintf("That is not the correct answer; option %d is correct.", correct+1)
				followUp = wrongQuizAnswerFollowUp
			}
			if explanation = sanitizePollField(explanation); explanation != "" {
				verdict += " Explanation to give if useful: " + explanation
			}
		}
	}

	return fmt.Sprintf("%s answered option %d (%q) of the quiz %q. %s %s %s",
		who, answerIndex+1, answerText, question, verdict, followUp, pollNoteOnce)
}

// sanitizePollField flattens participant-supplied text so it cannot break the
// [SYSTEM CONTEXT] frame it is quoted in.
func sanitizePollField(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "[", "(", "]", ")").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func (m *AIAgentModel) startStopTimer(roomId string) {
	m.timerMu.Lock()
	defer m.timerMu.Unlock()

	if _, ok := m.stopTimers[roomId]; ok {
		return
	}
	m.stopTimers[roomId] = time.AfterFunc(aiAgentEmptyRoomGrace, func() {
		m.timerMu.Lock()
		delete(m.stopTimers, roomId)
		m.timerMu.Unlock()

		if len(m.onlineHumans(roomId)) > 0 {
			return
		}
		m.stopEmptyRoom(roomId)
	})
}

func (m *AIAgentModel) cancelStopTimer(roomId string) {
	m.timerMu.Lock()
	defer m.timerMu.Unlock()

	if timer, ok := m.stopTimers[roomId]; ok {
		timer.Stop()
		delete(m.stopTimers, roomId)
	}
}
