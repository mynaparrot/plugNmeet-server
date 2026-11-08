package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// This file holds the curated plain English text of the integrations which do not
// render the client locales: the Auth API (/auth), LTI (/lti) and BBB
// (/:apiKey/bigbluebutton/api) endpoints. Their callers are integrators, so the
// text is short English and deliberately not an i18n key. Client-facing i18n keys
// live in api_error_msgs.go; the two must never be mixed.

// ErrAuthS2S* are the API-KEY/HASH-SIGNATURE failures of the server-to-server
// /auth endpoints. Their callers are integrators, not browsers, so the messages
// are short curated English text and deliberately not i18n keys: keep them out of
// the client locales.
var (
	ErrAuthS2SApiKeyMissing       = errors.New("API key is missing")
	ErrAuthS2SApiKeyInvalid       = errors.New("API key is invalid")
	ErrAuthS2SSignatureMissing    = errors.New("hash signature is missing")
	ErrAuthS2SSignatureInvalid    = errors.New("hash signature could not be verified")
	ErrAuthS2SRoomIdHeaderMissing = errors.New("Room-Id header is missing")
)

// The LTI launch errors are internal control sentinels; their text is only logged
// server-side and never sent to the LTI consumer.
var (
	InvalidConsumerKey    = errors.New("invalid consumer_key")
	VerificationFailed    = errors.New("verification failed")
	UserIdOrEmailRequired = errors.New("either value of user_id or lis_person_contact_email_primary  required")
)

// MsgRoomIdRequired is the plain error text when an Auth API request omits room_id.
const MsgRoomIdRequired = "missing required field room_id"

// MsgFile* are the file messages of the endpoints whose callers do not render the
// client locales: browser file downloads and the Auth API (integrators). They are
// short curated English text, deliberately not i18n keys.
const (
	MsgFileDownloadNotFound      = "File not found."
	MsgFileDownloadTokenInvalid  = "The download link is invalid or has expired."
	MsgFileDownloadInvalidPath   = "Invalid file path."
	MsgFileDownloadTempForbidden = "Access to the temporary directory is forbidden."
	MsgFileDownloadFailed        = "Failed to download the file."
	MsgFileInvalidRequest        = "Invalid file request."
	MsgFileUploadInProgress      = "Another whiteboard file upload is already in progress for this room."
	MsgFileInvalidDocumentLink   = "The document_link value is not a valid URL."
	MsgFileTooLarge              = "The file is larger than the maximum allowed size."
	MsgFileTypeNotAllowed        = "This file type is not allowed."
	MsgFileUploadFailed          = "The file upload failed."
	MsgFileConversionFailed      = "The file could not be converted."
	MsgFileRoomNotActive         = "The room is not active."
)

// MsgFileDownloadNotFoundNamed is the browser answer when a verified file has
// disappeared before it could be served. The placeholder receives a basename
// only, never a directory path.
const MsgFileDownloadNotFoundNamed = "File not found: %s."

// FileDownloadNotFoundMsg returns the not-found text for the basename of path, so
// no directory layout leaks into the response, and falls back to the generic
// message when no usable name is left.
func FileDownloadNotFoundMsg(path string) string {
	name := filepath.Base(path)
	if path == "" || name == "." || name == string(filepath.Separator) {
		return MsgFileDownloadNotFound
	}
	return fmt.Sprintf(MsgFileDownloadNotFoundNamed, name)
}

// MsgIntegratorUnexpected is the fallback answer of an integrator request. It is
// returned for anything that is not a known, safe message, so raw operational
// error text never leaves the server.
const MsgIntegratorUnexpected = "The request could not be completed."

// MsgIntegratorSuccess is the success message of an integrator response. The
// client success notice is an i18n key, so it must not be sent to integrators.
const MsgIntegratorSuccess = "success"

// Auth API (/auth) messages.
const (
	MsgAuthInvalidRequest              = "Invalid request."
	MsgAuthRoomNotFound                = "The room was not found."
	MsgAuthRoomNotActive               = "The room is not active."
	MsgAuthRoomIdNotAllowed            = "The room_id is not allowed."
	MsgAuthRoomCreateFailed            = "The room could not be created."
	MsgAuthPreassignedLimit            = "Too many pre-assigned breakout rooms were requested."
	MsgAuthPreassignedUser             = "A pre-assigned breakout room user is not valid."
	MsgAuthRoomEndFailed               = "The room could not be ended."
	MsgAuthBroadcastFailed             = "The message could not be broadcast to the room."
	MsgAuthNoOnlineUser                = "No user is online in the room."
	MsgAuthNoOnlineAdmin               = "No administrator is online in the room."
	MsgAuthOnlyAdmin                   = "Only an administrator can perform this action."
	MsgAuthUserNotFound                = "The user was not found in the room."
	MsgAuthFeatureNotAllowed           = "This feature is not enabled for the room."
	MsgAuthInvalidUrl                  = "The url is not valid."
	MsgAuthPastRoomsFetchFailed        = "The past rooms could not be fetched."
	MsgAuthUserInfoRequired            = "user_info is required."
	MsgAuthUserBlocked                 = "The user is blocked in this room."
	MsgAuthJoinTokenFailed             = "The join token could not be generated."
	MsgAuthReservedName                = "The user name is reserved."
	MsgAuthReservedUserId              = "The user_id is reserved."
	MsgAuthInvalidUserId               = "The user_id is not valid."
	MsgAuthPollCreateFailed            = "The poll could not be created."
	MsgAuthPollQuestionRequired        = "A poll question is required."
	MsgAuthPollMinOptions              = "A poll needs at least two options."
	MsgAuthPollOptionRequired          = "A poll option text is required."
	MsgAuthPollQuizNeedsCorrect        = "A quiz poll requires a correct option."
	MsgAuthPollDurationCap             = "The poll duration is longer than allowed."
	MsgAuthRecordingNotFound           = "The recording was not found."
	MsgAuthRequestedRecordingsNotFound = "One or more of the requested recordings were not found."
	MsgAuthRecordingsFetchFailed       = "The recordings could not be fetched."
	MsgAuthRecordingUpdateFailed       = "The recording could not be updated."
	MsgAuthRecordingDeleteFailed       = "The recording could not be deleted."
	MsgAuthRecordingMergeFailed        = "The recordings could not be merged."
	MsgAuthDownloadTokenFailed         = "The download token could not be generated."
	MsgAuthArtifactNotFound            = "The artifact was not found."
	MsgAuthArtifactsFetchFailed        = "The artifacts could not be fetched."
	MsgAuthArtifactDeleteFailed        = "The artifact could not be deleted."
	MsgAuthAnalyticsFetchFailed        = "The analytics could not be fetched."
)

// LTI (/lti) messages.
const (
	MsgLTIAuthorizationHeaderMissing = "Authorization header is missing."
	MsgLTIInvalidAuthorizationHeader = "The authorization header is not valid."
	MsgLTIOnlyAdmin                  = "Only an administrator can perform this action."
	MsgLTIPostRequired               = "Please use POST request."
	MsgLTIRoomIdMissing              = "Room id is missing."
	MsgLTIEmptyBody                  = "The request body is empty."
	MsgLTIInvalidRequest             = "The request is not valid."
	MsgLTILaunchVerificationFailed   = "The launch request could not be verified."
	MsgLTIRoomNotFound               = "The room was not found."
	MsgLTIRoomNotActive              = "The room is not active."
	MsgLTIRoomJoinFailed             = "The room could not be joined."
	MsgLTIRoomEndFailed              = "The room could not be ended."
	MsgLTIRecordingNotFound          = "The recording was not found."
	MsgLTIRecordingFetchFailed       = "The recording could not be fetched."
	MsgLTIRecordingsFetchFailed      = "The recordings could not be fetched."
	MsgLTIRecordingDeleteFailed      = "The recording could not be deleted."
	MsgLTIDownloadTokenFailed        = "The download token could not be generated."
)

// BBB (/:apiKey/bigbluebutton/api) messages.
const (
	MsgBBBApiKeyInvalid         = "invalid api key"
	MsgBBBChecksumMismatch      = "Checksums do not match"
	MsgBBBUnsupportedMethod     = "unsupported http method"
	MsgBBBParsingFailed         = "We can not parse request"
	MsgBBBInvalidRequest        = "The request is not valid."
	MsgBBBMeetingNotActive      = "The meeting is not active."
	MsgBBBPasswordMissing       = "password missing"
	MsgBBBExtraDataMissing      = "did not found extra data"
	MsgBBBUserBlocked           = "this user is blocked to join this session"
	MsgBBBCreateFailed          = "The meeting could not be created."
	MsgBBBJoinFailed            = "The meeting could not be joined."
	MsgBBBEndFailed             = "The meeting could not be ended."
	MsgBBBMeetingInfoFailed     = "The meeting information could not be fetched."
	MsgBBBRecordingsFetchFailed = "The recordings could not be fetched."
	MsgBBBRecordingDeleteFailed = "The recording could not be deleted."
	MsgBBBRoomNotFound          = "The meeting was not found."
)

// integratorMsgs maps a message that a browser client receives as an i18n key (or
// an internal sentinel that can reach an integrator response) to its curated
// English text.
var integratorMsgs = map[string]string{
	MsgSuccess:                             MsgIntegratorSuccess,
	ErrRoomInvalidRequest.Error():          MsgAuthInvalidRequest,
	ErrRoomIdRequired.Error():              MsgRoomIdRequired,
	ErrRoomNotFound.Error():                MsgAuthRoomNotFound,
	ErrRoomNotActive.Error():               MsgAuthRoomNotActive,
	ErrRoomUnexpectedError.Error():         MsgIntegratorUnexpected,
	ErrRoomEndFailed.Error():               MsgAuthRoomEndFailed,
	ErrRoomIdNotAllowed.Error():            MsgAuthRoomIdNotAllowed,
	ErrRoomPreassignedLimit.Error():        MsgAuthPreassignedLimit,
	ErrRoomPreassignedUser.Error():         MsgAuthPreassignedUser,
	ErrUserInvalidRequest.Error():          MsgAuthInvalidRequest,
	ErrUserInfoRequired.Error():            MsgAuthUserInfoRequired,
	ErrAuthUserBlocked.Error():             MsgAuthUserBlocked,
	ErrUserUnexpectedError.Error():         MsgIntegratorUnexpected,
	ErrUserRoomInfoNotFound.Error():        MsgAuthRoomNotFound,
	ErrUserRoomNotRunning.Error():          MsgAuthRoomNotActive,
	ErrUserRoomEnded.Error():               MsgAuthRoomNotActive,
	ErrUserReservedName.Error():            MsgAuthReservedName,
	ErrUserReservedUserId.Error():          MsgAuthReservedUserId,
	ErrUserInvalidUserId.Error():           MsgAuthInvalidUserId,
	ErrPollQuestionRequired.Error():        MsgAuthPollQuestionRequired,
	ErrPollMinOptions.Error():              MsgAuthPollMinOptions,
	ErrPollOptionRequired.Error():          MsgAuthPollOptionRequired,
	ErrPollQuizNeedsCorrect.Error():        MsgAuthPollQuizNeedsCorrect,
	ErrPollDurationCap.Error():             MsgAuthPollDurationCap,
	ErrPollGeneric.Error():                 MsgAuthPollCreateFailed,
	ErrRecordingRoomNotFound.Error():       MsgAuthRoomNotFound,
	ErrRecordingNotFound.Error():           MsgAuthRecordingNotFound,
	ErrRequestedRecordingsNotFound.Error(): MsgAuthRequestedRecordingsNotFound,
	NoOnlineUserFound.Error():              MsgAuthNoOnlineUser,
	NoOnlineAdminFound.Error():             MsgAuthNoOnlineAdmin,
	ErrRoomOnlyAdmin.Error():               MsgAuthOnlyAdmin,
	ErrRoomUserNotFound.Error():            MsgAuthUserNotFound,
	ErrRoomFeatureNotAllowed.Error():       MsgAuthFeatureNotAllowed,
	ErrRoomInvalidUrl.Error():              MsgAuthInvalidUrl,
}

// IntegratorMsg returns the curated English text for a message which was produced
// for a browser client, i.e. a client i18n key. Unknown messages, including raw
// operational error text, fall back to MsgIntegratorUnexpected; handlers must log
// the raw error server-side before answering.
func IntegratorMsg(msg string) string {
	if m, ok := integratorMsgs[msg]; ok {
		return m
	}
	return MsgIntegratorUnexpected
}
