package models

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
)

type EtherpadModel struct {
	SharedNotePad config.SharedNotePad
	NodeId        string
	Host          string
	ApiKey        string
	context       context.Context
	rc            *redis.Client
	rs            *RoomService
}

type EtherpadHttpRes struct {
	Code    int64             `json:"code"`
	Message string            `json:"message"`
	Data    EtherpadDataTypes `json:"data"`
}

type EtherpadDataTypes struct {
	AuthorID   string `json:"authorID"`
	GroupID    string `json:"groupID"`
	SessionID  string `json:"sessionID"`
	PadID      string `json:"padID"`
	ReadOnlyID string `json:"readOnlyID"`
}

const (
	APIVersion  = "1.2.15"
	EtherpadKey = "pnm:etherpad:"
)

func NewEtherpadModel() *EtherpadModel {
	return &EtherpadModel{
		rc:            config.AppCnf.RDS,
		context:       context.Background(),
		SharedNotePad: config.AppCnf.SharedNotePad,
		rs:            NewRoomService(),
	}
}

type CreateSessionRes struct {
	PadId         string
	ReadOnlyPadId string
}

// CreateSession will create group, pad, session
// return padId, readonlyPadId
func (m *EtherpadModel) CreateSession(roomId string) (*CreateSessionRes, error) {
	if len(m.SharedNotePad.EtherpadHosts) < 1 {
		return nil, errors.New("need at least one etherpad host")
	}
	m.selectHost()
	res := new(CreateSessionRes)
	res.PadId = uuid.NewString()

	// step 1: create pad using session id
	r, err := m.createPad(res.PadId)
	if err != nil {
		return nil, err
	}
	if r.Code > 0 {
		return nil, errors.New(r.Message)
	}

	// step 2: create readonly pad
	r, err = m.createReadonlyPad(res.PadId)
	if err != nil {
		return nil, err
	}
	if r.Code > 0 {
		return nil, errors.New(r.Message)
	}
	res.ReadOnlyPadId = r.Data.ReadOnlyID

	// add roomId to redis for this node
	m.rc.SAdd(m.context, EtherpadKey+m.NodeId, roomId)

	// finally, update to room
	_ = m.addPadToRoomMetadata(roomId, res)

	return res, nil
}

func (m *EtherpadModel) addPadToRoomMetadata(roomId string, c *CreateSessionRes) error {
	room, err := m.rs.LoadRoomInfoFromRedis(roomId)
	if err != nil {
		return err
	}

	meta := make([]byte, len(room.Metadata))
	copy(meta, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(meta, roomMeta)

	f := SharedNotePadFeatures{
		AllowedSharedNotePad: roomMeta.Features.SharedNotePadFeatures.AllowedSharedNotePad,
		IsActive:             true,
		NodeId:               m.NodeId,
		Host:                 m.Host,
		NotePadId:            c.PadId,
		ReadOnlyPadId:        c.ReadOnlyPadId,
	}
	roomMeta.Features.SharedNotePadFeatures = f

	metadata, err := json.Marshal(roomMeta)
	if err != nil {
		return err
	}

	_, _ = m.rs.UpdateRoomMetadata(roomId, string(metadata))

	return nil
}

type CleanPadReq struct {
	RoomId string `json:"room_id" validate:"required"`
	NodeId string `json:"node_id" validate:"required"`
	PadId  string `json:"pad_id" validate:"required"`
}

// CleanPad will delete group, session & pad
func (m *EtherpadModel) CleanPad(roomId, nodeId, padId string) error {
	for _, h := range m.SharedNotePad.EtherpadHosts {
		if h.Id == nodeId {
			m.Host = h.Host
			m.ApiKey = h.ApiKey
		}
	}
	if m.Host == "" {
		return errors.New("no host found")
	}

	// step 1: delete pad
	vals := url.Values{}
	vals.Add("padID", padId)
	_, _ = m.postToEtherpad("deletePad", vals)

	// add roomId to redis for this node
	_ = m.rc.SRem(m.context, EtherpadKey+nodeId, roomId)

	return nil
}

func (m *EtherpadModel) CleanAfterRoomEnd(roomId, metadata string) error {
	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal([]byte(metadata), roomMeta)

	np := roomMeta.Features.SharedNotePadFeatures
	if !np.AllowedSharedNotePad {
		return nil
	}

	err := m.CleanPad(roomId, np.NodeId, np.NotePadId)

	return err
}

type ChangeEtherpadStatusReq struct {
	RoomId   string `json:"room_id" validate:"required"`
	IsActive bool   `json:"is_active"`
}

func (m *EtherpadModel) ChangeEtherpadStatus(r *ChangeEtherpadStatusReq) error {
	room, err := m.rs.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return err
	}

	meta := make([]byte, len(room.Metadata))
	copy(meta, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(meta, roomMeta)

	roomMeta.Features.SharedNotePadFeatures.IsActive = r.IsActive
	metadata, err := json.Marshal(roomMeta)
	if err != nil {
		return err
	}

	_, err = m.rs.UpdateRoomMetadata(r.RoomId, string(metadata))

	return err
}

func (m *EtherpadModel) createPad(sessionId string) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)

	res, err := m.postToEtherpad("createPad", vals)
	return res, err
}

func (m *EtherpadModel) createReadonlyPad(sessionId string) (*EtherpadHttpRes, error) {
	vals := url.Values{}
	vals.Add("padID", sessionId)

	res, err := m.postToEtherpad("getReadOnlyID", vals)
	return res, err
}

// selectHost will choose server based on simple active number
func (m *EtherpadModel) selectHost() {
	type host struct {
		i      int
		id     string
		active int64
	}
	var hosts []host

	for i, h := range m.SharedNotePad.EtherpadHosts {
		c := m.rc.SCard(m.context, EtherpadKey+h.Id)
		hosts = append(hosts, host{
			i:      i,
			id:     h.Id,
			active: c.Val(),
		})
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].active < hosts[j].active
	})

	selectedHost := m.SharedNotePad.EtherpadHosts[hosts[0].i]
	m.NodeId = selectedHost.Id
	m.Host = selectedHost.Host
	m.ApiKey = selectedHost.ApiKey
}

func (m *EtherpadModel) postToEtherpad(method string, vals url.Values) (*EtherpadHttpRes, error) {
	endPoint := m.Host + "/api/" + APIVersion + "/" + method
	vals.Add("apikey", m.ApiKey)

	en := vals.Encode()
	resp, err := http.Get(endPoint + "?" + en)
	if err != nil {
		return nil, errors.New("can't connect to host")
	}

	if resp.Status != "200 OK" {
		return nil, errors.New("error code: " + resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	mar := new(EtherpadHttpRes)
	err = json.Unmarshal(body, mar)
	if err != nil {
		return nil, err
	}

	return mar, nil
}
