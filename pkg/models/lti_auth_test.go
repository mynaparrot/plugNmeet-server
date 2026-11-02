package models

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jordic/lti"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

const (
	testApiKey     = "test-api-key"
	testSecret     = "test-secret-at-least-32-bytes-long!"
	testSigningURL = "https://example.com/lti/v1"
)

func newTestLtiModel() *LtiV1Model {
	return &LtiV1Model{
		app: &config.AppConfig{
			Client: config.ClientInfo{ApiKey: testApiKey, Secret: testSecret},
		},
		logger: logrus.New().WithField("model", "lti_v1"),
	}
}

// signLtiRequest builds a url-encoded LTI 1.0 launch body with a valid oauth_signature.
func signLtiRequest(t *testing.T, consumerKey string, params url.Values) string {
	t.Helper()

	p := lti.NewProvider(testSecret, testSigningURL)
	p.Method = "POST"
	p.ConsumerKey = consumerKey
	for k, v := range params {
		p.Add(k, v[0])
	}
	if _, err := p.Sign(); err != nil {
		t.Fatalf("failed to sign request: %v", err)
	}

	signed := p.Params()
	parts := make([]string, 0, len(signed))
	for k := range signed {
		parts = append(parts, k+"="+url.QueryEscape(signed.Get(k)))
	}

	return strings.Join(parts, "&")
}

func TestVerifyAuthMalformedBody(t *testing.T) {
	m := newTestLtiModel()

	for _, body := range []string{"", "foo", "a=1&&b", "=", "oauth_consumer_key"} {
		if _, err := m.VerifyAuth(body, testSigningURL); err == nil {
			t.Fatalf("expected error for body %q", body)
		}
	}
}

func TestVerifyAuthRejectsInvalidConsumerKey(t *testing.T) {
	m := newTestLtiModel()
	body := signLtiRequest(t, "another-consumer-key", url.Values{"user_id": {"user-1"}})

	_, err := m.VerifyAuth(body, testSigningURL)
	if err != config.InvalidConsumerKey {
		t.Fatalf("expected InvalidConsumerKey, got %v", err)
	}
}

func TestVerifyAuthRejectsInvalidSignature(t *testing.T) {
	m := newTestLtiModel()
	body := signLtiRequest(t, testApiKey, url.Values{"user_id": {"user-1"}})

	params, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("failed to parse signed body: %v", err)
	}
	params.Set("oauth_signature", "tampered-signature")

	if _, err := m.VerifyAuth(params.Encode(), testSigningURL); err != config.VerificationFailed {
		t.Fatalf("expected VerificationFailed, got %v", err)
	}
}

func TestVerifyAuthAcceptsValidSignature(t *testing.T) {
	m := newTestLtiModel()
	body := signLtiRequest(t, testApiKey, url.Values{
		"lti_message_type":     {"basic-lti-launch-request"},
		"lti_version":          {"LTI-1p0"},
		"resource_link_id":     {"link-1"},
		"user_id":              {"user-1"},
		"lis_person_name_full": {"Jane Doe"},
		"roles":                {"Instructor"},
		// value containing "=" must survive the body parsing
		"custom_extra": {"a=b"},
	})

	params, err := m.VerifyAuth(body, testSigningURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for k, want := range map[string]string{
		"user_id":              "user-1",
		"lis_person_name_full": "Jane Doe",
		"roles":                "Instructor",
		"custom_extra":         "a=b",
	} {
		if got := params.Get(k); got != want {
			t.Fatalf("param %s: got %q, want %q", k, got, want)
		}
	}
}

func TestLtiJWTClaimsRoundTrip(t *testing.T) {
	m := newTestLtiModel()
	claims := &plugnmeet.LtiClaims{
		UserId:    "user-1",
		Name:      "Jane Doe",
		IsAdmin:   true,
		RoomId:    "room-1",
		RoomTitle: "Course 101",
		LtiCustomParameters: &plugnmeet.LtiCustomParameters{
			RoomDuration: proto.Uint64(3600),
			AllowPolls:   proto.Bool(true),
			MuteOnStart:  proto.Bool(false),
			LtiCustomDesign: &plugnmeet.LtiCustomDesign{
				PrimaryColor: proto.String("#123456"),
			},
		},
	}

	token, err := m.ToJWT(claims)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	got, err := m.LTIV1VerifyHeaderToken(token)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}

	if got.UserId != claims.UserId || got.Name != claims.Name || got.IsAdmin != claims.IsAdmin ||
		got.RoomId != claims.RoomId || got.RoomTitle != claims.RoomTitle {
		t.Fatalf("claims mismatch: got %+v", got)
	}
	if !proto.Equal(got.LtiCustomParameters, claims.LtiCustomParameters) {
		t.Fatalf("custom parameters mismatch: got %v", got.LtiCustomParameters)
	}
}

func TestLTIV1VerifyHeaderTokenRejectsRoomAccessToken(t *testing.T) {
	m := newTestLtiModel()

	token, err := auth.GeneratePlugNmeetJWTAccessToken(testApiKey, testSecret, "user-1", time.Hour, &plugnmeet.PlugNmeetTokenClaims{
		UserId: "user-1",
		RoomId: "room-1",
	})
	if err != nil {
		t.Fatalf("failed to generate access token: %v", err)
	}

	if _, err = m.LTIV1VerifyHeaderToken(token); err == nil {
		t.Fatal("expected room access token to be rejected as LTI token")
	}
}

func TestVerifyPlugNmeetAccessTokenRejectsLtiToken(t *testing.T) {
	m := newTestLtiModel()

	token, err := m.ToJWT(&plugnmeet.LtiClaims{
		UserId:    "user-1",
		Name:      "Jane Doe",
		RoomId:    "room-1",
		RoomTitle: "Course 101",
	})
	if err != nil {
		t.Fatalf("failed to generate lti token: %v", err)
	}

	if _, err = auth.VerifyPlugNmeetAccessToken(testApiKey, testSecret, token, 0); err == nil {
		t.Fatal("expected lti token to be rejected as room access token")
	}
}
