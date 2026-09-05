package models

import (
	"sort"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

// HandleSessionDataSave stores an encrypted session-scoped value (e.g. whiteboard
// or notepad data) sent by an authorized client. It is fire-and-forget: no response
// is sent back to the client.
func (m *NatsModel) HandleSessionDataSave(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	header, dataType, ok := m.parseAndAuthorizeSessionData(roomId, userId, req, false)
	if !ok {
		return
	}

	key := header.GetKey()
	if key == "" || len(req.BinMsg) == 0 {
		m.logger.Warnf("invalid session data save request from user %s; missing key or payload", userId)
		return
	}

	// Target-room routing for client-mediated breakout seeding.
	if target := header.GetTargetRoomId(); target != "" {
		if !m.canSeedSessionDataTo(roomId, target, userId) {
			return // already logged inside
		}
		if err := m.rs.SaveSessionData(target, dataType, key, req.BinMsg); err != nil {
			m.logger.WithError(err).Errorln("failed to save session data to target room")
		}
		return
	}

	if err := m.rs.SaveSessionData(roomId, dataType, key, req.BinMsg); err != nil {
		m.logger.WithError(err).Errorln("failed to save session data")
	}
}

// HandleSessionDataFetchRequest fetches either a single session data entry (when
// the header key is set) or all entries of a data type (when the key is unset)
// and streams the results back to the requesting user.
func (m *NatsModel) HandleSessionDataFetchRequest(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	header, dataType, ok := m.parseAndAuthorizeSessionData(roomId, userId, req, true)
	if !ok {
		return
	}

	key := header.GetKey()
	if key != "" {
		value, err := m.rs.GetSessionData(roomId, dataType, key)
		if err != nil {
			m.logger.WithError(err).Errorln("failed to fetch session data")
			return
		}
		m.sendSessionDataResponse(roomId, userId, dataType, key, value, true)
		return
	}

	values, err := m.rs.GetAllSessionData(roomId, dataType)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to fetch all session data")
		return
	}
	if len(values) == 0 {
		m.sendSessionDataResponse(roomId, userId, dataType, "", nil, true)
		return
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, k := range keys {
		m.sendSessionDataResponse(roomId, userId, dataType, k, []byte(values[k]), i == len(keys)-1)
	}
}

// canSeedSessionDataTo verifies that a SESSION_DATA_SAVE carrying a target_room_id
// is allowed to write into the target room. It returns true only when BOTH hold:
//  1. The sender is an ADMIN of the room the message arrived on (parentRoomId).
//     parseAndAuthorizeSessionData already gated them as a parent-room
//     presenter/admin for the data type; this adds the stricter admin requirement
//     for cross-room writes.
//  2. The target is a live breakout room DESCENDED from that room.
func (m *NatsModel) canSeedSessionDataTo(parentRoomId, targetRoomId, userId string) bool {
	sender, err := m.natsService.GetUserInfo(parentRoomId, userId)
	if err != nil || sender == nil || !sender.GetIsAdmin() {
		m.logger.Warnf("rejected session data seeding from user %s; not an admin of room %s", userId, parentRoomId)
		return false
	}

	meta, err := m.natsService.GetRoomMetadataStruct(targetRoomId)
	if err != nil || meta == nil || !meta.GetIsBreakoutRoom() || meta.GetParentRoomId() != parentRoomId {
		m.logger.Warnf("rejected session data seeding to room %s from user %s; not a breakout room of %s", targetRoomId, userId, parentRoomId)
		return false
	}

	return true
}

// parseAndAuthorizeSessionData parses the protojson SessionDataHeader from the
// message, validates the data type, and checks that the requesting user is allowed
// to access it. It returns the header, the proto SessionDataType, and whether
// the request is valid and authorized.
//
// isFetch indicates whether the request is a fetch (read) or a save (write).
// For fetch requests, authorization is relaxed for breakout rooms: a breakout room
// may never get an admin/presenter (no admin ever joined), yet its members must be
// able to hydrate seeded content, so any in-room member is allowed to FETCH. Saving
// new session data remains presenter/admin-gated to match normal rooms.
func (m *NatsModel) parseAndAuthorizeSessionData(roomId, userId string, req *plugnmeet.NatsMsgClientToServer, isFetch bool) (*plugnmeet.SessionDataHeader, plugnmeet.SessionDataType, bool) {
	header := new(plugnmeet.SessionDataHeader)
	if err := protojson.Unmarshal([]byte(req.Msg), header); err != nil {
		m.logger.WithError(err).Errorln("error unmarshalling session data header")
		return nil, plugnmeet.SessionDataType_SESSION_DATA_TYPE_UNSPECIFIED, false
	}

	// Relax fetch authorization for breakout rooms: members must be able to read
	// seeded content even though the room may have no presenter/admin.
	relaxForBreakout := false
	if isFetch {
		if meta, err := m.natsService.GetRoomMetadataStruct(roomId); err == nil && meta != nil {
			relaxForBreakout = meta.GetIsBreakoutRoom()
		}
	}

	authorized := false
	switch header.GetDataType() {
	case plugnmeet.SessionDataType_SESSION_DATA_TYPE_WHITEBOARD:
		authorized = m.natsService.IsUserPresenter(roomId, userId)
		if !authorized && relaxForBreakout {
			if userInfo, err := m.natsService.GetUserInfo(roomId, userId); err == nil && userInfo != nil {
				authorized = true
			}
		}
	case plugnmeet.SessionDataType_SESSION_DATA_TYPE_NOTEPAD:
		if userInfo, err := m.natsService.GetUserInfo(roomId, userId); err == nil && userInfo != nil {
			authorized = userInfo.GetIsPresenter() || userInfo.GetIsAdmin()
		}
		if !authorized && relaxForBreakout {
			if userInfo, err := m.natsService.GetUserInfo(roomId, userId); err == nil && userInfo != nil {
				authorized = true
			}
		}
	default:
		m.logger.Warnf("invalid session data type from user %s", userId)
		return nil, plugnmeet.SessionDataType_SESSION_DATA_TYPE_UNSPECIFIED, false
	}

	if !authorized {
		m.logger.Warnf("unauthorized session data request from user %s; room: %s", userId, roomId)
		return nil, plugnmeet.SessionDataType_SESSION_DATA_TYPE_UNSPECIFIED, false
	}
	return header, header.GetDataType(), true
}

// sendSessionDataResponse sends a single SESSION_DATA_FETCH_RESPONSE back to a
// specific user. The header carries the data entry key along with the binary value.
func (m *NatsModel) sendSessionDataResponse(roomId, userId string, dataType plugnmeet.SessionDataType, key string, value []byte, last bool) {
	header := &plugnmeet.SessionDataHeader{
		DataType: dataType,
		Key:      &key,
		Last:     last,
	}
	msg, err := protojson.Marshal(header)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to marshal session data header")
		return
	}

	err = m.natsService.BroadcastSystemEventToRoomWithBinMsg(plugnmeet.NatsMsgServerToClientEvents_SESSION_DATA_FETCH_RESPONSE, roomId, string(msg), value, &userId)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to send session data response")
	}
}
