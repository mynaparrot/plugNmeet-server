package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
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
		"roomId":      roomInfo.RoomId,
		"recordingId": r.RecordingId,
		"filePath":    r.FilePath,
		"method":      "addRecordingInfoFile",
	})
	log.Infoln("creating recording info file")
	toRecord := &plugnmeet.RecordingInfoFile{
		RoomTableId:      r.RoomTableId,
		RoomId:           roomInfo.RoomId,
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

	p := path.Join(m.app.RecorderInfo.RecordingFilesPath, filepath.Dir(r.FilePath))
	if _, err := os.Stat(p); err != nil && errors.Is(err, os.ErrNotExist) {
		// this can be expected when using hook system as file was uploaded and deleted
		// in this case we'll use temporary dir for hook
		p, err = os.MkdirTemp(m.app.RecorderInfo.RecordingFilesPath, "recording-meta")
		if err != nil {
			log.WithError(err).Errorln("failed to create temporary directory")
			return
		}
		// we should clean up as this was created only for hook script
		defer os.RemoveAll(p)
	}

	p = path.Join(p, filepath.Base(r.FilePath)+".json")
	if err = os.WriteFile(p, marshal, 0644); err != nil {
		log.WithError(err).Errorln("failed to write recording info file")
		return
	}

	log.Infoln("Successfully created recording info file")

	// run upload hook
	m.runUploadHook(roomInfo.RoomId, roomInfo.Sid, roomInfo.ID, p, log)
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
		"method": "MergeRecordings",
	})

	var recordings []dbmodels.Recording
	var roomInfo *dbmodels.RoomInfo // This will be populated in both successful cases.
	var roomId, roomSid string
	var roomTableId int64

	switch v := req.MergeScope.(type) {
	case *plugnmeet.MergeRecordingsReq_BySession:
		{
			log = log.WithField("room_sid", v.BySession.GetRoomSid())

			roomInfo, err = m.ds.GetRoomInfoBySid(v.BySession.GetRoomSid(), nil)
			if err != nil {
				log.WithError(err).Error("failed to get room info")
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
			}
			if roomInfo == nil {
				return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
			}
			// Set context for the rest of the function
			roomId = roomInfo.RoomId
			roomSid = roomInfo.Sid
			roomTableId = int64(roomInfo.ID)

			recs, total, err := m.ds.GetRecordings(nil, &roomInfo.Sid, 0, 0, nil)
			if err != nil {
				log.WithError(err).Error("failed to get recordings")
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
			}
			if total == 0 {
				return plugnmeet.StatusCode_NOT_FOUND, config.ErrRecordingNotFound
			}

			excludeRecordingIds := make(map[string]bool)
			for _, id := range v.BySession.GetExcludeRecordingIds() {
				excludeRecordingIds[id] = true
			}

			for _, r := range recs {
				if found, _ := excludeRecordingIds[r.RecordID]; found {
					continue
				}
				recordings = append(recordings, r)
			}
		}
	case *plugnmeet.MergeRecordingsReq_ByIds:
		{
			log = log.WithField("room_id", v.ByIds.GetRoomId())
			roomId = v.ByIds.GetRoomId()

			recordings, err = m.ds.GetRecordingsByIDs(v.ByIds.GetRecordingIds(), v.ByIds.GetRoomId())
			if err != nil {
				log.WithError(err).Error("failed to get recordings by ids")
				// Check for our new specific error.
				if errors.Is(err, config.ErrRequestedRecordingsNotFound) {
					return plugnmeet.StatusCode_NOT_FOUND, err
				}
				// Fallback for other potential database errors.
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
			}

			// Use the session ID from the LAST recording in the list.
			lastRecording := recordings[len(recordings)-1]
			roomSid = lastRecording.RoomSid.String

			// We also need the room table ID from that session for the transcoding task.
			roomInfo, err = m.ds.GetRoomInfoBySid(roomSid, nil)
			if err != nil {
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fmt.Errorf("could not fetch room info for sid: %s", roomSid)
			}
			// We MUST have roomInfo here.
			if roomInfo == nil {
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fmt.Errorf("data integrity issue: room session not found for sid %s", roomSid)
			}
			roomTableId = int64(roomInfo.ID)
		}
	}

	lockKey := fmt.Sprintf(redisservice.MergeRecordingReqLockKey, roomId)
	lock := m.rs.NewLock(lockKey, 24*time.Hour) // lock for 24 hours
	locked, err := lock.TryLock(ctx)
	if err != nil {
		log.WithError(err).Error("failed to acquire lock")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, "failed to acquire lock")
	}
	if !locked {
		log.Warn("another merge request is already in progress")
		return plugnmeet.StatusCode_CONFLICT, fiber.NewError(fiber.StatusConflict, "another request for this scope already registered, rejecting new one")
	}
	defer func() {
		if err != nil {
			// if error other than timeout then we'll unlock as it's real error
			_ = lock.Unlock(context.Background())
		}
	}()
	// unlock will be from ProcessRecorderEvent method when we'll receive final notification

	log = log.WithFields(logrus.Fields{
		"table_id": roomTableId,
		"room_id":  roomId,
	})

	var filePaths []string
	for _, r := range recordings {
		filePaths = append(filePaths, r.FilePath)
	}

	if len(filePaths) < 2 { // A merge requires at least two files.
		return plugnmeet.StatusCode_INVALID_PARAMETERS, fmt.Errorf("at least two recordings are required to merge")
	}

	task := &plugnmeet.TranscodingTask{
		RecordingId: fmt.Sprintf("%s-%d", roomSid, time.Now().UnixMilli()),
		RecorderId:  "merged-recording",
		RoomId:      roomId,
		RoomSid:     roomSid,
		RoomTableId: roomTableId,
		TaskDetails: &plugnmeet.TranscodingTask_MergeRecordings{
			MergeRecordings: &plugnmeet.TranscodingTaskMergeRecordings{
				FilePaths: filePaths,
			},
		},
	}
	data, err := proto.Marshal(task)
	if err != nil {
		log.WithError(err).Error("Failed to marshal transcoding task")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}

	// we'll also use lockKey just for preventing repeated request, default: 2 minutes
	pubAck, err := m.app.JetStream.Publish(ctx, m.app.NatsInfo.Recorder.TranscodingJobs, data, jetstream.WithMsgID(lockKey))
	if err != nil {
		log.WithError(err).Error("failed to publish message to NATS")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, err
	}
	if pubAck.Duplicate {
		return plugnmeet.StatusCode_CONFLICT, fmt.Errorf("another request (%d) for this scope already registered, rejecting new one", pubAck.Sequence)
	}

	log.Infof("Successfully registered new recordings merge task (%d) with %d files", pubAck.Sequence, len(filePaths))

	return plugnmeet.StatusCode_SUCCESS, nil
}
