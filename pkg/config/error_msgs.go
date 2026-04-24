package config

import "errors"

var UserNotActive = errors.New("user isn't active now")
var InvalidConsumerKey = errors.New("invalid consumer_key")
var VerificationFailed = errors.New("verification failed")
var UserIdOrEmailRequired = errors.New("either value of user_id or lis_person_contact_email_primary  required")
var NoOnlineUserFound = errors.New("no online user found")
var NotFoundErr = errors.New("not found")
