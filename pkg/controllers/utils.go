package controllers

import (
	"buf.build/go/protovalidate"
	"github.com/gofiber/fiber/v3"
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
