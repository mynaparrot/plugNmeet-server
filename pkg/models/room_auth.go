package models

import (
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"time"
)

type RoomAuthModel struct {
	rs *RoomService
	rm *RoomModel
}

func NewRoomAuthModel() *RoomAuthModel {
	return &RoomAuthModel{
		rs: NewRoomService(),
		rm: NewRoomModel(),
	}
}

func (am *RoomAuthModel) CreateRoom(r *plugnmeet.CreateRoomReq) (bool, string, *livekit.Room) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id > 0 {
		rf, err := am.rs.LoadRoomInfo(r.RoomId)
		if err != nil && err.Error() != "requested room does not exist" {
			return false, "can't create room. try again", nil
		}

		if err == nil && rf.Sid == roomDbInfo.Sid {
			return true, "room already exists", rf
		}

		// we'll allow to create room again & use the same DB row
		// we can just update the DB row. No need to create new one
	}

	// we'll set default values otherwise client got confused if data is missing
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, config.AppCnf.UploadFileSettings.MaxSize, config.AppCnf.UploadFileSettings.AllowedTypes, config.AppCnf.SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)

	// copyright
	if config.AppCnf.Client.CopyrightConf == nil {
		r.Metadata.CopyrightConf = &plugnmeet.CopyrightConf{
			Display: true,
			Text:    "Powered by <a href=\"https://www.plugnmeet.org\" target=\"_blank\">plugNmeet</a>",
		}
	} else {
		r.Metadata.CopyrightConf = config.AppCnf.Client.CopyrightConf
	}

	meta, err := json.Marshal(r.Metadata)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	room, err := am.rs.CreateRoom(r.RoomId, r.EmptyTimeout, r.MaxParticipants, string(meta))
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	isBreakoutRoom := 0
	if r.Metadata.IsBreakoutRoom {
		isBreakoutRoom = 1
	}

	updateTable := false
	ri := &RoomInfo{
		RoomTitle:          r.Metadata.RoomTitle,
		RoomId:             room.Name,
		Sid:                room.Sid,
		JoinedParticipants: 0,
		IsRunning:          1,
		CreationTime:       room.CreationTime,
		Created:            time.Now().Format("2006-01-02 15:04:05"),
		WebhookUrl:         "",
		IsBreakoutRoom:     int64(isBreakoutRoom),
		ParentRoomId:       r.Metadata.ParentRoomId,
	}
	if r.Metadata.WebhookUrl != nil {
		ri.WebhookUrl = *r.Metadata.WebhookUrl
	}

	if roomDbInfo.Id > 0 {
		updateTable = true
		ri.Id = roomDbInfo.Id
	}

	_, err = am.rm.InsertOrUpdateRoomData(ri, updateTable)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	return true, "room created", room
}

func (am *RoomAuthModel) IsRoomActive(r *plugnmeet.IsRoomActiveReq) (bool, string) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room is not active"
	}

	// let's make sure room actually active
	_, err := am.rs.LoadRoomInfo(r.RoomId)
	if err != nil {
		// room isn't active. Change status
		_, _ = am.rm.UpdateRoomStatus(&RoomInfo{
			RoomId:    r.RoomId,
			IsRunning: 0,
			Ended:     time.Now().Format("2006-01-02 15:04:05"),
		})
		return false, "room is not active"
	}

	return true, "room is active"
}

func (am *RoomAuthModel) GetActiveRoomInfo(r *plugnmeet.GetActiveRoomInfoReq) (bool, string, *plugnmeet.ActiveRoomInfoRes) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "no room found", nil
	}

	rrr, err := am.rs.LoadRoomInfo(r.RoomId)
	if err != nil {
		return false, err.Error(), nil
	}

	res := new(plugnmeet.ActiveRoomInfoRes)
	res.RoomInfo = &plugnmeet.ActiveRoomInfo{
		RoomTitle:          roomDbInfo.RoomTitle,
		RoomId:             roomDbInfo.RoomId,
		Sid:                roomDbInfo.Sid,
		JoinedParticipants: roomDbInfo.JoinedParticipants,
		IsRunning:          int32(roomDbInfo.IsRunning),
		IsRecording:        int32(roomDbInfo.IsRecording),
		IsActiveRtmp:       int32(roomDbInfo.IsActiveRTMP),
		WebhookUrl:         roomDbInfo.WebhookUrl,
		IsBreakoutRoom:     int32(roomDbInfo.IsBreakoutRoom),
		ParentRoomId:       roomDbInfo.ParentRoomId,
		CreationTime:       roomDbInfo.CreationTime,
		Metadata:           rrr.Metadata,
	}
	res.ParticipantsInfo, _ = am.rs.LoadParticipants(roomDbInfo.RoomId)

	return true, "success", res
}

func (am *RoomAuthModel) GetActiveRoomsInfo() (bool, string, []*plugnmeet.ActiveRoomInfoRes) {
	roomsInfo, err := am.rm.GetActiveRoomsInfo()

	if err != nil {
		return false, "no active room found", nil
	}

	if len(roomsInfo) == 0 {
		return false, "no active room found", nil
	}

	var res []*plugnmeet.ActiveRoomInfoRes
	for _, r := range roomsInfo {
		roomInfo := r
		i := new(plugnmeet.ActiveRoomInfoRes)
		i.RoomInfo = roomInfo

		participants, err := am.rs.LoadParticipants(r.RoomId)
		if err == nil {
			i.ParticipantsInfo = participants
		}

		rri, err := am.rs.LoadRoomInfo(r.RoomId)
		if err == nil {
			i.RoomInfo.Metadata = rri.Metadata
		}

		res = append(res, i)
	}

	return true, "success", res
}

func (am *RoomAuthModel) EndRoom(r *plugnmeet.RoomEndReq) (bool, string) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room not active"
	}

	_, err := am.rs.EndRoom(r.RoomId)
	if err != nil {
		return false, "can't end room"
	}

	_, _ = am.rm.UpdateRoomStatus(&RoomInfo{
		RoomId:    r.RoomId,
		IsRunning: 0,
		Ended:     time.Now().Format("2006-01-02 15:04:05"),
	})

	return true, "success"
}

func (am *RoomAuthModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	_, roomMeta, err := am.rs.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return false, err.Error()
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	_, err = am.rs.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}
