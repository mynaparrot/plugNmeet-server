package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// GetRoomFilesByType retrieves all file metadata for a given room, filtered by file type.
func (m *FileModel) GetRoomFilesByType(roomId string, fileType plugnmeet.RoomUploadedFileType) (*plugnmeet.GetRoomUploadedFilesRes, error) {
	allFiles, err := m.natsService.GetAllRoomFiles(roomId)
	if err != nil {
		return nil, err
	}

	if allFiles == nil {
		// Return an empty slice instead of nil for better client-side handling
		return nil, fmt.Errorf("no files found for room")
	}

	filteredFiles := make([]*plugnmeet.RoomUploadedFileMetadata, 0, len(allFiles))
	for _, meta := range allFiles {
		if meta.FileType == fileType {
			filteredFiles = append(filteredFiles, meta)
		}
	}

	return &plugnmeet.GetRoomUploadedFilesRes{
		Status: true,
		Msg:    "success",
		Files:  filteredFiles,
	}, nil
}
