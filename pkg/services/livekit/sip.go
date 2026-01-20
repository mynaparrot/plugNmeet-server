package livekitservice

import (
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"
)

const SipInboundTrunkIdRedisKey = "pnm:sip_inbound_trunk_id"

// CreateSIPInboundTrunk should call after bootup
func (s *LivekitService) CreateSIPInboundTrunk() error {
	sipInfo := s.app.LivekitSipInfo

	listReq := &livekit.ListSIPInboundTrunkRequest{
		Numbers: sipInfo.PhoneNumbers,
	}
	sipClient := lksdk.NewSIPClient(s.app.LivekitInfo.Host, s.app.LivekitInfo.ApiKey, s.app.LivekitInfo.Secret)
	trunks, err := sipClient.ListSIPInboundTrunk(s.ctx, listReq)
	if err != nil {
		return err
	}

	sipTrunkId := ""
	if trunks != nil {
		for _, item := range trunks.GetItems() {
			if item.Name == sipInfo.TrunkName {
				sipTrunkId = item.SipTrunkId
				break
			}
		}
	}

	trunkInfo := &livekit.SIPInboundTrunkInfo{
		Name:            sipInfo.TrunkName,
		Numbers:         sipInfo.PhoneNumbers,
		MediaEncryption: sipInfo.MediaEncryption,
	}
	if sipInfo.AllowedIpAddresses != nil && len(*sipInfo.AllowedIpAddresses) > 0 {
		trunkInfo.AllowedAddresses = *sipInfo.AllowedIpAddresses
	}
	if sipInfo.AuthUsername != nil && sipInfo.AuthPassword != nil {
		trunkInfo.AuthUsername = *sipInfo.AuthUsername
		trunkInfo.AuthPassword = *sipInfo.AuthPassword
	}

	if sipTrunkId == "" {
		_, err := sipClient.CreateSIPInboundTrunk(s.ctx, &livekit.CreateSIPInboundTrunkRequest{
			Trunk: trunkInfo,
		})
		if err != nil {
			return err
		}
		s.logger.Infof("sip trunk created successfully with id: %s", sipTrunkId)
	} else {
		request := &livekit.UpdateSIPInboundTrunkRequest{
			SipTrunkId: sipTrunkId,
			Action: &livekit.UpdateSIPInboundTrunkRequest_Replace{
				Replace: trunkInfo,
			},
		}
		_, err := sipClient.UpdateSIPInboundTrunk(s.ctx, request)
		if err != nil {
			return err
		}
		s.logger.Infof("sip trunk updated successfully with id: %s", sipTrunkId)
	}

	err = s.app.RDS.Set(s.ctx, SipInboundTrunkIdRedisKey, sipTrunkId, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func (s *LivekitService) CreateSIPDispatchRule(roomId string, hidePhoneNumber bool, log *logrus.Entry) (ruleId string, pin string, err error) {
	sipTrunkId, err := s.app.RDS.Get(s.ctx, SipInboundTrunkIdRedisKey).Result()
	if err != nil {
		return "", "", err
	}
	if sipTrunkId == "" {
		log.Errorln("sip trunk id not found in redis")
		return "", "", fmt.Errorf("sip trunk id not found")
	}

	pin = helpers.GenerateSipPin(6)
	rule := &livekit.SIPDispatchRule{
		Rule: &livekit.SIPDispatchRule_DispatchRuleDirect{
			DispatchRuleDirect: &livekit.SIPDispatchRuleDirect{
				RoomName: roomId,
				Pin:      pin,
			},
		},
	}
	request := &livekit.CreateSIPDispatchRuleRequest{
		DispatchRule: &livekit.SIPDispatchRuleInfo{
			Rule:            rule,
			Name:            roomId,
			HidePhoneNumber: hidePhoneNumber,
			TrunkIds:        []string{sipTrunkId},
		},
	}

	sipClient := lksdk.NewSIPClient(s.app.LivekitInfo.Host, s.app.LivekitInfo.ApiKey, s.app.LivekitInfo.Secret)
	dispatchRule, err := sipClient.CreateSIPDispatchRule(s.ctx, request)
	if err != nil {
		return "", "", err
	}

	log.Infof("sip dispatch rule created successfully with id: %s", dispatchRule.SipDispatchRuleId)
	return dispatchRule.SipDispatchRuleId, pin, nil
}

func (s *LivekitService) DeleteSIPDispatchRule(roomId string, log *logrus.Entry) {
	sipClient := lksdk.NewSIPClient(s.app.LivekitInfo.Host, s.app.LivekitInfo.ApiKey, s.app.LivekitInfo.Secret)
	rules, err := sipClient.ListSIPDispatchRule(s.ctx, &livekit.ListSIPDispatchRuleRequest{})
	if err != nil {
		log.WithError(err).Error("error listing sip dispatch rules")
		return
	}
	if rules == nil {
		return
	}

	for _, item := range rules.GetItems() {
		if item.Name == roomId {
			log.Infof("sip dispatch rule found with id: %s, deleting it", item.SipDispatchRuleId)
			_, err := sipClient.DeleteSIPDispatchRule(s.ctx, &livekit.DeleteSIPDispatchRuleRequest{SipDispatchRuleId: item.SipDispatchRuleId})
			if err != nil {
				log.WithError(err).Error("error deleting sip dispatch rule")
				return
			}
		}
	}
}
