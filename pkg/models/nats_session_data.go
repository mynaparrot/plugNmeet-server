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
	header, dataType, ok := m.parseAndAuthorizeSessionData(roomId, userId, req)
	if !ok {
		return
	}

	key := header.GetKey()
	if key == "" || len(req.BinMsg) == 0 {
		m.logger.Warnf("invalid session data save request from user %s; missing key or payload", userId)
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
	header, dataType, ok := m.parseAndAuthorizeSessionData(roomId, userId, req)
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

// parseAndAuthorizeSessionData parses the protojson SessionDataHeader from the
// message, validates the data type, and checks that the requesting user is allowed
// to access it. It returns the header, the proto SessionDataType, and whether
// the request is valid and authorized.
func (m *NatsModel) parseAndAuthorizeSessionData(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) (*plugnmeet.SessionDataHeader, plugnmeet.SessionDataType, bool) {
	header := new(plugnmeet.SessionDataHeader)
	if err := protojson.Unmarshal([]byte(req.Msg), header); err != nil {
		m.logger.WithError(err).Errorln("error unmarshalling session data header")
		return nil, plugnmeet.SessionDataType_SESSION_DATA_TYPE_UNSPECIFIED, false
	}

	authorized := false
	switch header.GetDataType() {
	case plugnmeet.SessionDataType_SESSION_DATA_TYPE_WHITEBOARD:
		authorized = m.natsService.IsUserPresenter(roomId, userId)
	case plugnmeet.SessionDataType_SESSION_DATA_TYPE_NOTEPAD:
		if userInfo, err := m.natsService.GetUserInfo(roomId, userId); err == nil && userInfo != nil {
			authorized = userInfo.GetIsPresenter() || userInfo.GetIsAdmin()
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
