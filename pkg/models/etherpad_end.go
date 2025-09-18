package models

import (
	"net/url"

	"github.com/sirupsen/logrus"
)

// CleanPad will delete the group, session & pad
func (m *EtherpadModel) CleanPad(roomId, nodeId, padId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"nodeId": nodeId,
		"padId":  padId,
		"method": "CleanPad",
	})
	log.Infoln("request to clean etherpad pad")

	var hostFound bool
	for _, h := range m.app.SharedNotePad.EtherpadHosts {
		if h.Id == nodeId {
			m.NodeId = nodeId
			m.Host = h.Host
			m.ClientId = h.ClientId
			m.ClientSecret = h.ClientSecret
			hostFound = true
			break
		}
	}
	if !hostFound {
		// this is normal if etherpad wasn't created
		log.Info("no host found for the given node id")
		return nil
	}

	// step 1: delete pad
	vals := url.Values{}
	vals.Add("padID", padId)
	_, err := m.postToEtherpad("deletePad", vals, log)
	if err != nil {
		// postToEtherpad will log the error details, so we just log a warning here.
		log.WithError(err).Warn("failed to delete pad from etherpad, continuing cleanup")
	}

	// add roomId to redis for this node
	if err = m.natsService.RemoveRoomFromEtherpad(nodeId, roomId); err != nil {
		log.WithError(err).Error("failed to remove room from etherpad nats store")
	}

	log.Info("successfully cleaned etherpad pad")
	return nil
}

func (m *EtherpadModel) CleanAfterRoomEnd(roomId, metadata string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"method": "CleanAfterRoomEnd",
	})

	if metadata == "" {
		return nil
	}

	roomMeta, err := m.natsService.UnmarshalRoomMetadata(metadata)
	if err != nil {
		log.WithError(err).Warn("could not unmarshal room metadata, skipping etherpad cleanup")
		return nil // Don't block cleanup for this
	}

	if roomMeta.GetRoomFeatures() == nil || roomMeta.GetRoomFeatures().GetSharedNotePadFeatures() == nil {
		return nil
	}

	np := roomMeta.RoomFeatures.SharedNotePadFeatures
	if !np.AllowedSharedNotePad {
		return nil
	}

	log.Info("triggering etherpad cleanup after room end")
	err = m.CleanPad(roomId, np.NodeId, np.NotePadId)
	return err
}
