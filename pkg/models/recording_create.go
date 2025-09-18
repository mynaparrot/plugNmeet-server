package models

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
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

func (m *RecordingModel) addRecordingInfoToDB(r *plugnmeet.RecorderToPlugNmeet, roomCreationTime int64) (int64, error) {
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
		Size:             helpers.ToFixed(float64(r.FileSize), 2),
		FilePath:         r.FilePath,
		RoomCreationTime: roomCreationTime,
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
	op := protojson.MarshalOptions{
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
