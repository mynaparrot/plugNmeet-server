package models

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// recordingStarted update when recorder will start recording
func (m *RecordingModel) recordingStarted(r *plugnmeet.RecorderToPlugNmeet) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"roomSid":     r.RoomSid,
		"recorderId":  r.RecorderId,
		"roomTableId": r.RoomTableId,
		"method":      "recordingStarted",
	})
	log.Infoln("processing recording_started event from recorder")

	_, err := m.ds.UpdateRoomRecordingStatus(uint64(r.RoomTableId), 1, &r.RecorderId)
	if err != nil {
		log.WithError(err).Errorln("error updating room recording status in db")
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return
	}
	if roomMeta == nil {
		log.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsRecording = true
	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	// send a notification message to room
	err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.recording-started", false, nil)
	if err != nil {
		log.WithError(err).Errorln("error sending notification message")
	}
	log.Infoln("finished processing recording_started event")
}

func (m *RecordingModel) addRecordingInfoToDB(r *plugnmeet.RecorderToPlugNmeet, roomInfo *dbmodels.RoomInfo) (int64, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"recordingId": r.RecordingId,
		"method":      "addRecordingInfoToDB",
	})
	log.Infoln("adding recording info to db")

	v := sql.NullString{
		String: r.RoomSid,
		Valid:  true,
	}

	data := &dbmodels.Recording{
		RecordID:         r.RecordingId,
		RoomID:           r.RoomId,
		RoomSid:          v,
		RecorderID:       r.RecorderId,
		Size:             fmt.Sprintf("%.2f", r.FileSize),
		FilePath:         r.FilePath,
		RoomCreationTime: roomInfo.CreationTime,
	}

	metadata := &plugnmeet.RecordingMetadata{
		Title: &roomInfo.RoomTitle,
		ExtraData: map[string]string{
			"meeting-title":   roomInfo.RoomTitle,
			"meeting-created": fmt.Sprintf(roomInfo.Created.Format(time.RFC3339)),
			"meeting-ended":   fmt.Sprint(roomInfo.Ended.Format(time.RFC3339)),
		},
	}
	if marshal, err := protojson.Marshal(metadata); err == nil {
		data.Metadata = string(marshal)
	}

	_, err := m.ds.InsertRecordingData(data)
	if err != nil {
		log.WithError(err).Errorln("failed to insert recording data")
		return 0, err
	}

	return data.CreationTime, nil
}

// addRecordingInfoFile will add information about the recording file
// there have a certain case that our DB may have a problem
// using this recording info file we can import those recordings
// or will get an idea about the recording
// format: path/recording_file_name.{mp4|webm}.json
func (m *RecordingModel) addRecordingInfoFile(r *plugnmeet.RecorderToPlugNmeet, creation int64, roomInfo *dbmodels.RoomInfo) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"recordingId": r.RecordingId,
		"filePath":    r.FilePath,
		"method":      "addRecordingInfoFile",
	})
	log.Infoln("creating recording info file")
	toRecord := &plugnmeet.RecordingInfoFile{
		RoomTableId:      r.RoomTableId,
		RoomId:           r.RoomId,
		RoomTitle:        roomInfo.RoomTitle,
		RoomSid:          roomInfo.Sid,
		RoomCreationTime: roomInfo.CreationTime,
		RoomEnded:        roomInfo.Ended.UnixMilli(),
		RecordingId:      r.RecordingId,
		RecorderId:       r.RecorderId,
		FilePath:         r.FilePath,
		FileSize:         r.FileSize,
		CreationTime:     creation,
	}
	var op = protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(toRecord)
	if err != nil {
		log.WithError(err).Errorln("failed to marshal recording info file data")
		return
	}
	path := fmt.Sprintf("%s/%s.json", m.app.RecorderInfo.RecordingFilesPath, r.FilePath)

	err = os.WriteFile(path, marshal, 0644)
	if err != nil {
		log.WithError(err).Errorln("failed to write recording info file")
		return
	}
	log.Infoln("successfully created recording info file")
}

// UpdateRecordingMetadata updates the metadata of a specific recording.
// It intelligently handles partial updates based on the provided fields:
// - To update a field, provide a new value.
// - To clear a text field (like Title), provide an empty string "".
// - To clear a specific map entry (like a subtitle), provide an empty object for that key.
// - If a field is omitted (i.e., nil), its existing value is kept.
//
// NOTE: If you add a new field to the RecordingMetadata protobuf,
// you must add a new block here to handle its update.
func (m *RecordingModel) UpdateRecordingMetadata(req *plugnmeet.UpdateRecordingMetadataReq) error {
	// 1. Fetch the existing recording info.
	recording, err := m.FetchRecording(req.GetRecordId())
	if err != nil {
		return err
	}

	// 2. Prepare the metadata struct.
	existingMeta := recording.Metadata
	if existingMeta == nil {
		existingMeta = &plugnmeet.RecordingMetadata{}
	}

	if req.Metadata == nil {
		return nil
	}

	var modified bool

	// Handle Title
	if req.Metadata.Title != nil {
		newValue := req.Metadata.Title
		if *newValue == "" {
			if existingMeta.Title != nil {
				existingMeta.Title = nil
				modified = true
			}
		} else {
			if existingMeta.Title == nil || *existingMeta.Title != *newValue {
				existingMeta.Title = newValue
				modified = true
			}
		}
	}

	// Handle Description
	if req.Metadata.Description != nil {
		newValue := req.Metadata.Description
		if *newValue == "" {
			if existingMeta.Description != nil {
				existingMeta.Description = nil
				modified = true
			}
		} else {
			if existingMeta.Description == nil || *existingMeta.Description != *newValue {
				existingMeta.Description = newValue
				modified = true
			}
		}
	}

	// Handle Subtitles (Patching)
	if req.Metadata.Subtitles != nil {
		for key, value := range req.Metadata.Subtitles {
			// A subtitle is empty for deletion if it's nil or has no label and no URL.
			isEmpty := value == nil || (value.Label == "" || value.Url == "")

			if isEmpty {
				// It's a delete request.
				if existingMeta.Subtitles != nil { // Can't delete from a nil map
					if _, ok := existingMeta.Subtitles[key]; ok {
						delete(existingMeta.Subtitles, key)
						modified = true
					}
				}
			} else {
				// It's an update/insert request.
				if existingMeta.Subtitles == nil {
					existingMeta.Subtitles = make(map[string]*plugnmeet.RecordingSubtitle)
				}

				if !reflect.DeepEqual(existingMeta.Subtitles[key], value) {
					existingMeta.Subtitles[key] = value
					modified = true
				}
			}
		}
	}

	// Handle ExtraData (Patching)
	if req.Metadata.ExtraData != nil {
		if existingMeta.ExtraData == nil {
			existingMeta.ExtraData = make(map[string]string)
		}
		for key, value := range req.Metadata.ExtraData {
			// If the value is empty, it's a request to delete.
			if value == "" {
				if _, ok := existingMeta.ExtraData[key]; ok {
					delete(existingMeta.ExtraData, key)
					modified = true
				}
			} else { // Otherwise, it's an update/insert.
				if existingMeta.ExtraData[key] != value {
					existingMeta.ExtraData[key] = value
					modified = true
				}
			}
		}
	}

	// 4. If no changes were made, we can exit early.
	if !modified {
		return nil
	}

	// 5. Marshal the newly updated metadata back into a JSON string.
	updatedMetadataBytes, err := protojson.Marshal(existingMeta)
	if err != nil {
		return fmt.Errorf("failed to marshal updated metadata: %w", err)
	}

	// 6. Save the updated JSON string to the database.
	err = m.ds.UpdateRecordingMetadata(recording.RecordId, string(updatedMetadataBytes))
	if err != nil {
		return fmt.Errorf("failed to save updated metadata to database: %w", err)
	}

	return nil
}

func (m *RecordingModel) MergeRecordings(ctx context.Context, req *plugnmeet.MergeRecordingsReq) (statusCode plugnmeet.StatusCode, err error) {
	log := m.logger.WithFields(logrus.Fields{
		"method":   "MergeRecordings",
		"room_sid": req.GetRoomSid(),
	})
	lockKey := fmt.Sprintf(redisservice.MergeRecordingReqLockKey, req.RoomSid)
	lock := m.rs.NewLock(lockKey, 24*time.Hour) // lock for 24 hours
	locked, err := lock.TryLock(ctx)
	if err != nil {
		log.WithError(err).Error("failed to acquire lock")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, "failed to acquire lock")
	}
	if !locked {
		log.Warn("another whiteboard file upload is already in progress")
		return plugnmeet.StatusCode_CONFLICT, fiber.NewError(fiber.StatusConflict, "another request from same roomSid already registered or proceeded successfully, rejecting new one")
	}
	defer func() {
		if err != nil {
			// if error other than timeout then we'll unlock as it's real error
			_ = lock.Unlock(context.Background())
		}
	}()
	// unlock will be for ProcessRecorderEvent method when we'll receive final notification

	roomInfo, err := m.ds.GetRoomInfoBySid(req.RoomSid, nil)
	if err != nil {
		log.WithError(err).Error("failed to get room info")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}
	if roomInfo == nil {
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}
	log = log.WithFields(logrus.Fields{
		"table_id": roomInfo.ID,
		"room_id":  roomInfo.RoomId,
	})

	recordings, total, err := m.ds.GetRecordings(nil, &roomInfo.Sid, 0, 0, nil)
	if err != nil {
		log.WithError(err).Error("failed to get recordings")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}
	if total == 0 {
		return plugnmeet.StatusCode_NOT_FOUND, config.ErrRecordingNotFound
	}

	excludeRecordingIds := make(map[string]bool)
	for _, id := range req.ExcludeRecordingIds {
		excludeRecordingIds[id] = true
	}

	var filePaths []string
	recorderId := "merged-recording" // for all this types
	for _, r := range recordings {
		if found, _ := excludeRecordingIds[r.RecordID]; found {
			continue
		}
		if r.RecorderID == recorderId {
			// this session already have one merged recording, so we' won't continue
			return plugnmeet.StatusCode_CONFLICT, fmt.Errorf("this session already have one merged recording, rejecting new request")
		}
		filePaths = append(filePaths, r.FilePath)
	}
	if len(filePaths) == 0 {
		return plugnmeet.StatusCode_NOT_FOUND, fmt.Errorf("no recordings found to merge")
	}

	task := &plugnmeet.TranscodingTask{
		RecordingId: fmt.Sprintf("%s-%d", req.RoomSid, time.Now().UnixMilli()),
		RecorderId:  recorderId,
		RoomId:      roomInfo.RoomId,
		RoomSid:     req.RoomSid,
		RoomTableId: int64(roomInfo.ID),
		TaskDetails: &plugnmeet.TranscodingTask_MergeRecordings{
			MergeRecordings: &plugnmeet.TranscodingTaskMergeRecordings{
				FilePaths: filePaths,
			},
		},
	}
	data, err := proto.Marshal(task)
	if err != nil {
		log.WithError(err).Errorln("Failed to marshal transcoding task")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}

	// we'll also use lockKey just for preventing repeated request, default: 2 minutes
	pubAck, err := m.app.JetStream.Publish(ctx, m.app.NatsInfo.Recorder.TranscodingJobs, data, jetstream.WithMsgID(lockKey))
	if err != nil {
		log.WithError(err).Error("failed to publish message to NATS")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}
	if pubAck.Duplicate {
		return plugnmeet.StatusCode_CONFLICT, fmt.Errorf("another request (%d) from same roomSid already registered or proceeded successfully, rejecting new one", pubAck.Sequence)
	}

	log.Infof("Successfully registered new recordings merge task (%d) with %d files", pubAck.Sequence, len(filePaths))

	return plugnmeet.StatusCode_SUCCESS, nil
}
