package models

import (
	"errors"
)

func (m *FileModel) updateRoomMetadataWithOfficeFile(roomId string, f *ConvertWhiteboardFileRes) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return errors.New("invalid nil room metadata information")
	}

	wbf := roomMeta.RoomFeatures.WhiteboardFeatures
	wbf.WhiteboardFileId = f.FileId
	wbf.FileName = f.FileName
	wbf.FilePath = f.FilePath
	wbf.TotalPages = uint32(f.TotalPages)

	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, roomMeta)
	if err != nil {
		m.logger.WithError(err).Errorln("metadata update failed")
	}

	return err
}
