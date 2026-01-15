package models

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
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
