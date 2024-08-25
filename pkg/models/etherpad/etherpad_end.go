package etherpadmodel

import (
	"errors"
	log "github.com/sirupsen/logrus"
	"net/url"
)

// CleanPad will delete the group, session & pad
func (m *EtherpadModel) CleanPad(roomId, nodeId, padId string) error {
	for _, h := range m.app.SharedNotePad.EtherpadHosts {
		if h.Id == nodeId {
			m.NodeId = nodeId
			m.Host = h.Host
			m.ClientId = h.ClientId
			m.ClientSecret = h.ClientSecret
		}
	}
	if m.Host == "" {
		return errors.New("no host found")
	}

	// step 1: delete pad
	vals := url.Values{}
	vals.Add("padID", padId)
	_, err := m.postToEtherpad("deletePad", vals)
	if err != nil {
		log.Errorln(err)
	}

	// add roomId to redis for this node
	_ = m.rs.RemoveRoomFromEtherpad(nodeId, roomId)

	return nil
}

func (m *EtherpadModel) CleanAfterRoomEnd(roomId, metadata string) error {
	if metadata == "" {
		return nil
	}

	roomMeta, _ := m.natsService.UnmarshalRoomMetadata(metadata)
	if roomMeta.GetRoomFeatures() == nil || roomMeta.GetRoomFeatures().GetSharedNotePadFeatures() == nil {
		return nil
	}

	np := roomMeta.RoomFeatures.SharedNotePadFeatures
	if !np.AllowedSharedNotePad {
		return nil
	}

	err := m.CleanPad(roomId, np.NodeId, np.NotePadId)
	return err
}
