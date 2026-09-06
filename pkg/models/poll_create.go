package models

import (
	"errors"
	"time"

	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *PollModel) CreatePoll(r *plugnmeet.CreatePollReq) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"method": "CreatePoll",
	})
	log.Infoln("request to create poll")

	if err := validateCreatePollReq(r); err != nil {
		log.WithError(err).Warn("invalid create poll request")
		return "", err
	}

	r.PollId = uuid.NewString()
	log = log.WithField("pollId", r.PollId)

	// create poll hash and add to room
	err := m.createRoomPollHash(r)
	if err != nil {
		log.WithError(err).Errorln("failed to create room poll hash")
		return "", config.ErrPollGeneric
	}

	// schedule server-side auto-close at expiry; lazy check covers restarts
	if r.Duration > 0 {
		time.AfterFunc(time.Duration(r.Duration)*time.Second, func() {
			_ = m.AutoClosePoll(r.RoomId, r.PollId)
		})
	}

	err = m.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_POLL_CREATED, r.RoomId, r.PollId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("error sending POLL_CREATED event")
	}

	// send analytics
	toRecord := struct {
		PollId   string                         `json:"poll_id"`
		Question string                         `json:"question"`
		Options  []*plugnmeet.CreatePollOptions `json:"options"`
	}{
		PollId:   r.PollId,
		Question: r.Question,
		Options:  r.Options,
	}
	marshal, err := json.Marshal(toRecord)
	if err != nil {
		log.WithError(err).Errorln("failed to marshal analytics data")
	}
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_ADDED,
		RoomId:    r.RoomId,
		HsetValue: new(string(marshal)),
	})

	log.Info("successfully created poll")
	return r.PollId, nil
}

// validateCreatePollReq enforces the server-side creation rules.
func validateCreatePollReq(r *plugnmeet.CreatePollReq) error {
	if r.Question == "" {
		return config.ErrPollQuestionRequired
	}
	if len(r.Options) < 2 {
		return config.ErrPollMinOptions
	}
	hasCorrect := false
	for _, opt := range r.Options {
		if opt.Text == "" {
			return config.ErrPollOptionRequired
		}
		if opt.IsCorrect {
			hasCorrect = true
		}
	}
	if r.IsQuiz && !hasCorrect {
		return config.ErrPollQuizNeedsCorrect
	}
	if r.Duration > config.MaxPollDurationSeconds {
		return config.ErrPollDurationCap
	}
	return nil
}

// createRoomPollHash will insert the poll to room hash
func (m *PollModel) createRoomPollHash(r *plugnmeet.CreatePollReq) error {
	created := time.Now().Unix()
	p := &plugnmeet.PollInfo{
		Id:          r.PollId,
		RoomId:      r.RoomId,
		Question:    r.Question,
		Options:     r.Options,
		IsRunning:   true,
		Created:     created,
		CreatedBy:   r.UserId,
		IsAnonymous: r.IsAnonymous,
		IsMultiple:  r.IsMultiple,
		IsQuiz:      r.IsQuiz,
	}
	// fixed duration polls get a hard expiry; 0 means no limit
	if r.Duration > 0 {
		p.Duration = r.Duration
		p.ExpiresAt = created + int64(r.Duration)
	}

	marshal, err := protojson.Marshal(p)
	if err != nil {
		return err
	}

	pollVal := map[string]string{
		r.PollId: string(marshal),
	}
	return m.rs.CreateRoomPoll(r.RoomId, pollVal)
}

func (m *PollModel) UserSubmitResponse(r *plugnmeet.SubmitPollResponseReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"pollId": r.PollId,
		"method": "UserSubmitResponse",
	})
	log.Infoln("request to submit poll response")

	// server-authoritative guard: poll must exist and still be running
	pi, err := m.rs.GetPollInfoByPollId(r.RoomId, r.PollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return config.ErrPollGeneric
	}
	if pi == "" {
		return config.ErrPollNotFound
	}

	info := new(plugnmeet.PollInfo)
	if err = protojson.Unmarshal([]byte(pi), info); err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return config.ErrPollGeneric
	}

	// lazy expiry check before accepting a vote (recovers restart-lost timers)
	m.maybeAutoCloseExpired(info)

	if !info.IsRunning {
		return config.ErrPollClosed
	}
	if len(r.SelectedOptions) == 0 {
		return config.ErrPollNoOptionSelected
	}
	// drop duplicate selections while preserving order
	r.SelectedOptions = dedupeSelectedOptions(r.SelectedOptions)

	// single-choice polls accept exactly one option
	if !info.IsMultiple && len(r.SelectedOptions) > 1 {
		return config.ErrPollGeneric
	}
	// reject option ids that don't belong to this poll
	validIds := make(map[uint64]struct{}, len(info.Options))
	for _, opt := range info.Options {
		validIds[uint64(opt.Id)] = struct{}{}
	}
	for _, id := range r.SelectedOptions {
		if _, ok := validIds[id]; !ok {
			return config.ErrPollGeneric
		}
	}

	err = m.rs.AddPollResponse(r, info.IsAnonymous)
	if err != nil {
		// double-voting is an expected user error; don't spam the log with it
		if errors.Is(err, config.ErrPollAlreadyVoted) {
			return err
		}
		log.WithError(err).Errorln("failed to add poll response to redis")
		return config.ErrPollGeneric
	}

	// send analytics; anonymous polls record participation only — never the choice
	toRecord := struct {
		PollId          string `json:"poll_id"`
		SelectedOptions string `json:"selected_options,omitempty"`
	}{
		PollId: r.PollId,
	}
	if !info.IsAnonymous {
		toRecord.SelectedOptions = config.JoinPollOptionIds(r.SelectedOptions)
	}
	marshal, err := json.Marshal(toRecord)
	if err != nil {
		log.WithError(err).Errorln("failed to marshal analytics data")
	}
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_VOTED_POLL,
		RoomId:    r.RoomId,
		UserId:    &r.UserId,
		HsetValue: new(string(marshal)),
	})

	log.Info("successfully submitted poll response")
	return nil
}

func (m *PollModel) ReopenPoll(r *plugnmeet.ReopenPollReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"pollId": r.PollId,
		"method": "ReopenPoll",
	})
	log.Infoln("request to reopen poll")

	duration, reopened, err := m.rs.ReopenPollIfClosed(r)
	if err != nil {
		// not-found is an expected user error; don't spam the log with it
		if errors.Is(err, config.ErrPollNotFound) {
			return err
		}
		log.WithError(err).Errorln("failed to reopen poll in redis")
		return config.ErrPollGeneric
	}
	if !reopened {
		// idempotent: already running — no side effects, no events
		log.Info("poll already running; nothing to reopen")
		return nil
	}

	// schedule server-side auto-close at expiry; lazy check covers restarts
	if duration > 0 {
		time.AfterFunc(time.Duration(duration)*time.Second, func() {
			_ = m.AutoClosePoll(r.RoomId, r.PollId)
		})
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_POLL_REOPENED, r.RoomId, r.PollId, nil)
	if err != nil {
		log.WithError(err).Errorln("error sending POLL_REOPENED event")
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_POLL_REOPENED,
		RoomId:    r.RoomId,
		HsetValue: &r.PollId,
	})

	log.Info("successfully reopened poll")
	return nil
}

// dedupeSelectedOptions drops duplicate option ids while preserving order.
func dedupeSelectedOptions(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
