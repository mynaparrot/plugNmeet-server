package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *EtherpadModel) ChangeEtherpadStatus(r *plugnmeet.ChangeEtherpadStatusReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"isActive": r.IsActive,
		"method":   "ChangeEtherpadStatus",
	})
	log.Infoln("request to change etherpad status")

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return err
	}
	if meta == nil {
		err = errors.New("invalid nil room metadata information")
		log.WithError(err).Errorln()
		return err
	}

	meta.RoomFeatures.SharedNotePadFeatures.IsActive = r.IsActive
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, meta)
	if err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_ETHERPAD_STATUS,
		RoomId:    r.RoomId,
		HsetValue: &val,
	}
	if !r.IsActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_ETHERPAD_STATUS
		d.HsetValue = &val
	}
	m.analyticsModel.HandleEvent(d)

	if err == nil {
		log.Info("successfully changed etherpad status")
	}
	return err
}

func (m *EtherpadModel) addPadToRoomMetadata(roomId string, selectedHost *config.EtherpadInfo, c *plugnmeet.CreateEtherpadSessionRes, log *logrus.Entry) error {
	log = log.WithField("method", "addPadToRoomMetadata")
	log.Info("adding pad info to room metadata")

	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return err
	}
	if meta == nil {
		err = errors.New("invalid room information")
		log.WithError(err).Errorln()
		return err
	}

	f := &plugnmeet.SharedNotePadFeatures{
		AllowedSharedNotePad: meta.RoomFeatures.SharedNotePadFeatures.AllowedSharedNotePad,
		IsActive:             true,
		NodeId:               selectedHost.Id,
		Host:                 selectedHost.Host,
		NotePadId:            *c.PadId,
		ReadOnlyPadId:        *c.ReadonlyPadId,
	}
	meta.RoomFeatures.SharedNotePadFeatures = f

	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, meta)
	if err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	// send analytics
	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_ETHERPAD_STATUS,
		RoomId:    roomId,
		HsetValue: &val,
	})

	if err == nil {
		log.Info("successfully added pad to room metadata")
	}
	return err
}

func (m *EtherpadModel) postToEtherpad(host *config.EtherpadInfo, method string, vals url.Values, log *logrus.Entry) (*EtherpadHttpRes, error) {
	log = log.WithField("etherpadMethod", method)

	if host.Id == "" {
		err := errors.New("no notepad nodeId found")
		log.WithError(err).Error()
		return nil, err
	}
	token, err := m.getAccessToken(host, log)
	if err != nil {
		// getAccessToken will log the error
		return nil, err
	}

	client := &http.Client{}
	en := vals.Encode()
	endPoint := fmt.Sprintf("%s/api/%s/%s?%s", host.Host, APIVersion, method, en)
	log.WithField("endpoint", endPoint).Debug("sending request to etherpad")

	ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endPoint, nil)
	if err != nil {
		log.WithError(err).Error("failed to create http request")
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		log.WithError(err).Error("http client execution failed")
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		err = fmt.Errorf("received non-200 status code: %s", res.Status)
		log.WithError(err).Error("etherpad API request failed")
		return nil, err
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.WithError(err).Error("failed to read response body")
		return nil, err
	}

	mar := new(EtherpadHttpRes)
	err = json.Unmarshal(body, mar)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal etherpad response")
		return nil, err
	}

	return mar, nil
}

func (m *EtherpadModel) getAccessToken(host *config.EtherpadInfo, log *logrus.Entry) (string, error) {
	token, _ := m.natsService.GetEtherpadToken(host.Id)
	if token != "" {
		log.Debug("using cached etherpad access token")
		return token, nil
	}
	log.Info("requesting new etherpad access token")

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", host.ClientId)
	data.Set("client_secret", host.ClientSecret)
	encodedData := data.Encode()

	client := &http.Client{}
	urlPath := fmt.Sprintf("%s/oidc/token", host.Host)

	req, err := http.NewRequest("POST", urlPath, strings.NewReader(encodedData))
	if err != nil {
		log.WithError(err).Error("failed to create http request for access token")
		return "", err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		log.WithError(err).Error("http client execution failed for access token")
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		err = fmt.Errorf("received non-200 status code for access token: %s", res.Status)
		log.WithError(err).Error()
		return "", err
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.WithError(err).Error("failed to read access token response body")
		return "", err
	}

	vals := struct {
		AccessToken string `json:"access_token"`
	}{}
	err = json.Unmarshal(body, &vals)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal access token response")
		return "", err
	}

	if vals.AccessToken == "" {
		err = fmt.Errorf("could not get access_token from response")
		log.WithError(err).Error()
		return "", err
	}

	// we'll store the value with expiry of 30-minute max
	err = m.natsService.AddEtherpadToken(host.Id, vals.AccessToken, time.Minute*30)
	if err != nil {
		log.WithError(err).Warn("failed to cache etherpad access token")
	}

	return vals.AccessToken, nil
}
