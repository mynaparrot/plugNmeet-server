package controllers

import (
	"buf.build/go/protovalidate"
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var unmarshalOpts = protojson.UnmarshalOptions{
	DiscardUnknown: true,
}

func parseAndValidateRequest(data []byte, msg proto.Message) error {
	err := unmarshalOpts.Unmarshal(data, msg)
	if err != nil {
		return err
	}
	return validateRequest(msg)
}

func validateRequest(msg proto.Message) error {
	v, err := protovalidate.New()
	if err != nil {
		return err
	}

	if err = v.Validate(msg); err != nil {
		return err
	}
	return nil
}

// callerMsg returns the message of a handler which can be reached by more than one
// kind of caller. Browser clients keep the client-facing i18n key (the client
// resolves it via t()); the Auth API, LTI and BBB integrations get curated plain
// English, with raw operational text replaced by a generic message. The
// classification comes from the trusted route middleware (config.LocalsCallerKey)
// and never from a client-supplied value.
//
// Pass only model/route produced messages (i18n keys, config sentinels) in here:
// the curated Msg*/MsgAuth*/MsgLTI*/MsgBBB* constants are already final text.
func callerMsg(c fiber.Ctx, msg string) string {
	if fiber.Locals[string](c, config.LocalsCallerKey) != config.CallerIntegrator {
		return msg
	}
	return config.IntegratorMsg(msg)
}

// callerMsgWithKey is callerMsg for the response envelopes which carry an
// optional msg_key: browser clients keep the client-facing i18n key as msg and
// get no msg_key, integrators get the curated English text and the key itself.
func callerMsgWithKey(c fiber.Ctx, msg string) (string, *string) {
	if fiber.Locals[string](c, config.LocalsCallerKey) != config.CallerIntegrator {
		return msg, nil
	}
	text, key := config.IntegratorMsgAndKey(msg)
	return text, &key
}

// sendCommonErrorJsonResponse sends a failed CommonResponse as protojson. msg is a
// client i18n key or a config sentinel; an integrator caller gets the curated
// English text plus the key in msg_key.
func sendCommonErrorJsonResponse(c fiber.Ctx, msg string, code plugnmeet.StatusCode) error {
	text, key := callerMsgWithKey(c, msg)
	return utils.SendCommonErrorJsonResponse(c, text, code, derefMsgKey(key))
}

// sendCommonProtoJsonResponse sends a CommonResponse as protojson.
func sendCommonProtoJsonResponse(c fiber.Ctx, status bool, msg string, code plugnmeet.StatusCode) error {
	text, key := callerMsgWithKey(c, msg)
	return utils.SendCommonProtoJsonResponse(c, status, text, code, derefMsgKey(key))
}

// sendCommonErrorProtobufResponse sends a failed CommonResponse as protobuf.
func sendCommonErrorProtobufResponse(c fiber.Ctx, msg string, code plugnmeet.StatusCode) error {
	text, key := callerMsgWithKey(c, msg)
	res := &plugnmeet.CommonResponse{
		Status:     false,
		Msg:        text,
		StatusCode: code,
		MsgKey:     key,
	}
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/protobuf")
	return c.Status(utils.CommonErrorHTTPStatus(code)).Send(marshal)
}

func derefMsgKey(key *string) string {
	if key == nil {
		return ""
	}
	return *key
}
