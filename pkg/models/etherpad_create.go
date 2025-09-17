package models

import (
	"errors"
	"net/url"
	"sort"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// CreateSession will create group, pad, session
// return padId, readonlyPadId
func (m *EtherpadModel) CreateSession(roomId, requestedUserId string) (*plugnmeet.CreateEtherpadSessionRes, error) {
	if len(m.app.SharedNotePad.EtherpadHosts) < 1 {
		return nil, errors.New("need at least one etherpad host")
	}
	err := m.selectHost()
	if err != nil {
		return nil, err
	}

	res := new(plugnmeet.CreateEtherpadSessionRes)
	pid := uuid.NewString()
	res.PadId = &pid

	// step 1: create pad using session id
	r, err := m.createPad(pid, requestedUserId)
	if err != nil {
		return nil, err
	}
	if r.Code > 0 {
		return nil, errors.New(r.Message)
	}

	// step 2: create readonly pad
	r, err = m.createReadonlyPad(pid)
	if err != nil {
		return nil, err
	}
	if r.Code > 0 {
		return nil, errors.New(r.Message)
	}
	res.ReadonlyPadId = &r.Data.ReadOnlyID

	// add roomId to redis for this node
	err = m.natsService.AddRoomInEtherpad(m.NodeId, roomId)
	if err != nil {
		m.logger.Errorln(err)
	}

	// finally, update to room
	err = m.addPadToRoomMetadata(roomId, res)
	if err != nil {
		m.logger.Errorln(err)
	}

	res.Status = true
	res.Msg = "success"
	return res, nil
}

func (m *EtherpadModel) createPad(sessionId, requestedUserId string) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)
	if requestedUserId != "" {
		vals.Add("authorId", requestedUserId)
	}

	res, err := m.postToEtherpad("createPad", vals)
	if err != nil {
		m.logger.Errorln(err)
	}
	return res, err
}

func (m *EtherpadModel) createReadonlyPad(sessionId string) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)

	res, err := m.postToEtherpad("getReadOnlyID", vals)
	if err != nil {
		m.logger.Errorln(err)
	}
	return res, err
}

// selectHost will choose server based on simple active number
func (m *EtherpadModel) selectHost() error {
	type host struct {
		i      int
		id     string
		active int64
	}
	var hosts []host

	for i, h := range m.app.SharedNotePad.EtherpadHosts {
		ok := m.checkStatus(h)
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
		return errors.New("no active etherpad host found")
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].active < hosts[j].active
	})

	selectedHost := m.app.SharedNotePad.EtherpadHosts[hosts[0].i]
	m.NodeId = selectedHost.Id
	m.Host = selectedHost.Host
	m.ClientId = selectedHost.ClientId
	m.ClientSecret = selectedHost.ClientSecret

	return nil
}

func (m *EtherpadModel) checkStatus(h config.EtherpadInfo) bool {
	m.NodeId = h.Id
	m.Host = h.Host
	m.ClientId = h.ClientId
	m.ClientSecret = h.ClientSecret

	vals := url.Values{}
	_, err := m.postToEtherpad("getStats", vals)
	if err != nil {
		m.logger.Errorln(err)
		return false
	}

	return true
}
