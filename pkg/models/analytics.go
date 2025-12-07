package models

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	analyticsRoomKey = redisservice.Prefix + "analytics:%s"
	analyticsUserKey = analyticsRoomKey + ":user:%s"
)

type AnalyticsModel struct {
	ctx             context.Context
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	natsService     *natsservice.NatsService
	webhookNotifier *helpers.WebhookNotifier
	logger          *logrus.Entry
	artifactModel   *ArtifactModel
}

func NewAnalyticsModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, logger *logrus.Logger) *AnalyticsModel {
	return &AnalyticsModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              rs,
		natsService:     natsService,
		webhookNotifier: webhookNotifier,
		logger:          logger.WithField("model", "analytics"),
	}
}

// SetArtifactModel sets the ArtifactModel to resolve the circular dependency.
// This will be called by the dependency injector.
func (m *AnalyticsModel) SetArtifactModel(am *ArtifactModel) {
	m.artifactModel = am
}

// insertEventData stores an analytics event in Redis based on its type.
// It uses HSET for events with timestamps and INCRBY or SET for simpler counter or string values.
func (m *AnalyticsModel) insertEventData(d *plugnmeet.AnalyticsDataMsg, key string) {
	if d.EventValueInteger == nil && d.EventValueString == nil {
		// so this will be HSET type
		var val map[string]string
		if d.HsetValue != nil {
			val = map[string]string{
				fmt.Sprintf("%d", d.Time): d.GetHsetValue(),
			}
		} else {
			val = map[string]string{
				fmt.Sprintf("%d", d.Time): fmt.Sprintf("%d", d.Time),
			}
		}
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.AddAnalyticsHSETType(k, val)
		if err != nil {
			m.logger.WithError(err).Errorln("AddAnalyticsHSETType failed")
		}

	} else if d.EventValueInteger != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.IncrementAnalyticsVal(k, d.GetEventValueInteger())
		if err != nil {
			m.logger.WithError(err).Errorln("IncrementAnalyticsVal failed")
		}
	} else if d.EventValueString != nil {
		// we are assuming that we want to set the supplied value
		// in this case we'll simply use SET, which will be string type
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.AddAnalyticsStringType(k, d.GetEventValueString())
		if err != nil {
			m.logger.WithError(err).Errorln("AddAnalyticsStringType failed")
		}
	}
}

// handleFirstTimeUserJoined records a user's information in Redis the first time they join.
// It also triggers the insertion of the user_joined event.
func (m *AnalyticsModel) handleFirstTimeUserJoined(d *plugnmeet.AnalyticsDataMsg, key string) {
	umeta := new(plugnmeet.UserMetadata)
	// Use getter for safety and log potential errors.
	if d.GetExtraData() != "" {
		var err error
		umeta, err = m.natsService.UnmarshalUserMetadata(d.GetExtraData())
		if err != nil {
			m.logger.WithError(err).Warn("failed to unmarshal user metadata for analytics")
		}
	}

	uInfo := &plugnmeet.AnalyticsRedisUserInfo{
		Name:     d.UserName,
		IsAdmin:  umeta.IsAdmin,
		ExUserId: umeta.ExUserId,
	}

	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(uInfo)
	if err != nil {
		m.logger.WithError(err).Errorln("marshalling failed")
		return // Don't proceed if marshalling fails.
	}

	u := map[string]string{
		*d.UserId: string(marshal),
	}
	k := fmt.Sprintf("%s:users", key)

	err = m.rs.AddAnalyticsUser(k, u)
	if err != nil {
		m.logger.WithError(err).Errorln("AddAnalyticsUser failed")
	}

	// Also, handle the user_joined event insertion here to avoid a second call.
	userEventKey := fmt.Sprintf(analyticsUserKey, d.RoomId, d.GetUserId())
	m.insertEventData(d, userEventKey)
}

// HandleEvent is the main entry point for processing incoming analytics events.
// It sets the event timestamp and routes the event to the appropriate handler based on its type.
func (m *AnalyticsModel) HandleEvent(d *plugnmeet.AnalyticsDataMsg) {
	if m.app.AnalyticsSettings == nil ||
		!m.app.AnalyticsSettings.Enabled {
		return
	}
	// we'll use unix milliseconds to make sure fields are unique
	d.Time = time.Now().UnixMilli()

	switch d.EventType {
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM:
		m.handleRoomTypeEvents(d)
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER:
		m.handleUserTypeEvents(d)
	}
}

// handleRoomTypeEvents processes events that are scoped to a room.
func (m *AnalyticsModel) handleRoomTypeEvents(d *plugnmeet.AnalyticsDataMsg) {
	if d.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsRoomKey+":room", d.RoomId)

	switch d.GetEventName() {
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED:
		m.handleFirstTimeUserJoined(d, key)
		// The user-specific event insertion is now handled inside handleFirstTimeUserJoined
	default:
		m.insertEventData(d, key)
	}
}

// handleUserTypeEvents processes events that are scoped to a specific user within a room.
func (m *AnalyticsModel) handleUserTypeEvents(d *plugnmeet.AnalyticsDataMsg) {
	if d.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, d.RoomId, d.GetUserId())
	m.insertEventData(d, key)
}

// FetchAnalytics retrieves a paginated list of analytics files.
// Deprecated: For backward compatibility, it fetches data from both the new artifacts system
// and the old analytics table, then merges and sorts the results.
func (m *AnalyticsModel) FetchAnalytics(r *plugnmeet.FetchAnalyticsReq) (*plugnmeet.FetchAnalyticsResult, error) {
	if r.Limit <= 0 {
		r.Limit = 20
	} else if r.Limit > 100 {
		r.Limit = 100
	}
	if r.OrderBy == "" {
		r.OrderBy = "DESC"
	}

	// 1. Fetch from the new artifacts system
	artifactType := plugnmeet.RoomArtifactType_MEETING_ANALYTICS
	artifacts, err := m.artifactModel.FetchArtifacts(&plugnmeet.FetchArtifactsReq{
		RoomIds: r.RoomIds,
		Type:    &artifactType,
		Limit:   uint64(r.Limit),
		From:    uint64(r.From),
		OrderBy: r.OrderBy,
	})
	if err != nil {
		return nil, err
	}

	var analytics []*plugnmeet.AnalyticsInfo
	for _, v := range artifacts.ArtifactsList {
		if v.Metadata == nil || v.Metadata.FileInfo == nil {
			continue
		}

		created, _ := time.Parse(time.RFC3339, v.Created)
		analytic := &plugnmeet.AnalyticsInfo{
			RoomId:       v.RoomId,
			FileId:       v.ArtifactId,
			FileSize:     float64(v.Metadata.FileInfo.FileSize),
			FileName:     filepath.Base(v.Metadata.FileInfo.FilePath),
			CreationTime: created.Unix(),
		}
		analytics = append(analytics, analytic)
	}

	// 2. Fetch from the old analytics table
	oldData, totalOld, err := m.ds.GetAnalytics(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}

	for _, v := range oldData {
		// Avoid duplicates - if it was already fetched from artifacts, skip it.
		isDuplicate := false
		for _, existing := range analytics {
			if existing.FileId == v.FileID {
				isDuplicate = true
				break
			}
		}
		if isDuplicate {
			continue
		}

		analytic := &plugnmeet.AnalyticsInfo{
			RoomId:       v.RoomID,
			FileId:       v.FileID,
			FileSize:     v.FileSize,
			FileName:     v.FileName,
			CreationTime: v.CreationTime,
		}
		analytics = append(analytics, analytic)
	}

	// 3. Sort the combined list
	sort.SliceStable(analytics, func(i, j int) bool {
		if r.OrderBy == "DESC" {
			return analytics[i].CreationTime > analytics[j].CreationTime
		}
		return analytics[i].CreationTime < analytics[j].CreationTime
	})

	// 4. Apply pagination to the merged list
	total := int64(len(analytics))
	start := int64(r.From)
	end := start + int64(r.Limit)
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	paginatedAnalytics := analytics[start:end]

	result := &plugnmeet.FetchAnalyticsResult{
		TotalAnalytics: totalOld + artifacts.TotalArtifacts,
		From:           r.From,
		Limit:          r.Limit,
		OrderBy:        r.OrderBy,
		AnalyticsList:  paginatedAnalytics,
	}

	return result, nil
}
