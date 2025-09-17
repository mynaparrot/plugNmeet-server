package models

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"google.golang.org/protobuf/encoding/protojson"
)

// recordingStarted update when recorder will start recording
func (m *RecordingModel) recordingStarted(r *plugnmeet.RecorderToPlugNmeet) {
	_, err := m.ds.UpdateRoomRecordingStatus(uint64(r.RoomTableId), 1, &r.RecorderId)
	if err != nil {
		m.logger.WithError(err).Infoln("error updating room recording status")
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return
	}
	if roomMeta == nil {
		m.logger.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsRecording = true
	_ = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	// send a notification message to room
	err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.recording-started", false, nil)
	if err != nil {
		m.logger.WithError(err).Errorln("error sending notification message")
	}
}

func (m *RecordingModel) addRecordingInfoToDB(r *plugnmeet.RecorderToPlugNmeet, roomCreationTime int64) (int64, error) {
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
		m.logger.WithError(err).Errorln("marshalling failed")
		return
	}
	path := fmt.Sprintf("%s/%s.json", m.app.RecorderInfo.RecordingFilesPath, r.FilePath)

	err = os.WriteFile(path, marshal, 0644)
	if err != nil {
		m.logger.WithError(err).Errorln("error writing file")
		return
	}
}
