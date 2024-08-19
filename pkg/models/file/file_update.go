package filemodel

import log "github.com/sirupsen/logrus"

func (m *FileModel) updateRoomMetadataWithOfficeFile(f *ConvertWhiteboardFileRes) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(m.req.RoomId)
	if err != nil {
		return err
	}

	roomMeta.RoomFeatures.WhiteboardFeatures.WhiteboardFileId = f.FileId
	roomMeta.RoomFeatures.WhiteboardFeatures.FileName = f.FileName
	roomMeta.RoomFeatures.WhiteboardFeatures.FilePath = f.FilePath
	roomMeta.RoomFeatures.WhiteboardFeatures.TotalPages = uint32(f.TotalPages)

	err = m.natsService.UpdateAndBroadcastRoomMetadata(m.req.RoomId, roomMeta)
	if err != nil {
		log.Errorln(err)
	}

	return err
}
