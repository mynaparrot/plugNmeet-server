package models

import (
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// CreateSession will create group, pad, session
// return padId, readonlyPadId
func (m *EtherpadModel) CreateSession(roomId, requestedUserId string) (*plugnmeet.CreateEtherpadSessionRes, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":          roomId,
		"requestedUserId": requestedUserId,
		"method":          "CreateSession",
	})
	log.Infoln("request to create etherpad session")

	if len(m.app.SharedNotePad.EtherpadHosts) < 1 {
		err := errors.New("need at least one etherpad host")
		log.WithError(err).Error()
		return nil, err
	}
	selectedHost, err := m.selectHost(log)
	if err != nil {
		// selectHost will log the error details
		return nil, err
	}

	res := new(plugnmeet.CreateEtherpadSessionRes)
	pid := uuid.NewString()
	res.PadId = &pid
	log = log.WithField("padId", pid)

	// step 1: create pad using session id
	r, err := m.createPad(selectedHost, pid, requestedUserId, log)
	if err != nil {
		// createPad logs the error
		return nil, err
	}
	if r.Code > 0 {
		err = errors.New(r.Message)
		log.WithError(err).Error("etherpad API returned error while creating pad")
		return nil, err
	}

	// step 2: create readonly pad
	r, err = m.createReadonlyPad(selectedHost, pid, log)
	if err != nil {
		// createReadonlyPad logs the error
		return nil, err
	}
	if r.Code > 0 {
		err = errors.New(r.Message)
		log.WithError(err).Error("etherpad API returned error while creating readonly pad")
		return nil, err
	}
	res.ReadonlyPadId = &r.Data.ReadOnlyID

	// add roomId to redis for this node
	err = m.natsService.AddRoomInEtherpad(selectedHost.Id, roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to add room to etherpad in nats")
	}

	// finally, update to room
	err = m.addPadToRoomMetadata(roomId, selectedHost, res, log)
	if err != nil {
		log.WithError(err).Errorln("failed to add pad to room metadata")
	}

	res.Status = true
	res.Msg = "success"
	log.Info("successfully created etherpad session")
	return res, nil
}

func (m *EtherpadModel) createPad(host *config.EtherpadInfo, sessionId, requestedUserId string, log *logrus.Entry) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)
	if requestedUserId != "" {
		vals.Add("authorId", requestedUserId)
	}

	return m.postToEtherpad(host, "createPad", vals, log)
}

func (m *EtherpadModel) createReadonlyPad(host *config.EtherpadInfo, sessionId string, log *logrus.Entry) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)

	return m.postToEtherpad(host, "getReadOnlyID", vals, log)
}

// selectHost will choose server based on simple active number
func (m *EtherpadModel) selectHost(log *logrus.Entry) (*config.EtherpadInfo, error) {
	log = log.WithField("method", "selectHost")
	log.Info("selecting an etherpad host")

	type host struct {
		i      int
		id     string
		active int64
	}
	var hosts []host

	for i, h := range m.app.SharedNotePad.EtherpadHosts {
		ok := m.checkStatus(h, log)
		if ok {
			c, _ := m.natsService.GetEtherpadActiveRoomsNum(h.Id)
			hosts = append(hosts, host{
				i:      i,
				id:     h.Id,
				active: c,
			})
		}
	}
	if len(hosts) == 0 {
		err := fmt.Errorf("no active etherpad host found")
		log.WithError(err).Error()
		return nil, err
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].active < hosts[j].active
	})

	selectedHost := m.app.SharedNotePad.EtherpadHosts[hosts[0].i]
	log.WithFields(logrus.Fields{
		"selectedHostId": selectedHost.Id,
		"host":           selectedHost.Host,
	}).Info("etherpad host selected")
	return &selectedHost, nil
}

func (m *EtherpadModel) checkStatus(h config.EtherpadInfo, log *logrus.Entry) bool {
	log = log.WithField("checkingHost", h.Host)

	vals := url.Values{}
	_, err := m.postToEtherpad(&h, "getStats", vals, log)
	if err != nil {
		// postToEtherpad will log the error
		log.Warn("etherpad host is not healthy")
		return false
	}

	return true
}
