package ltiv1model

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/jordic/lti"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"net/url"
	"strings"
	"time"
)

func (m *LtiV1Model) VerifyAuth(requests, signingURL string) (*url.Values, error) {
	r := strings.Split(requests, "&")
	p := lti.NewProvider(config.GetConfig().Client.Secret, signingURL)
	p.Method = "POST"
	p.ConsumerKey = config.GetConfig().Client.ApiKey
	var providedSignature string

	for _, f := range r {
		t := strings.Split(f, "=")
		b, _ := url.QueryUnescape(t[1])
		if t[0] == "oauth_signature" {
			providedSignature = b
		} else {
			p.Add(t[0], b)
		}
	}

	if p.Get("oauth_consumer_key") != p.ConsumerKey {
		return nil, errors.New(config.InvalidConsumerKey)
	}

	sign, err := p.Sign()
	if err != nil {
		return nil, err
	}
	params := p.Params()

	if sign != providedSignature {
		log.Errorln("Calculated: " + sign + " provided: " + providedSignature)
		return nil, errors.New(config.VerificationFailed)
	}

	return &params, nil
}

func (m *LtiV1Model) genHashId(id string) string {
	hasher := sha1.New()
	hasher.Write([]byte(id))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return hash
}

func (m *LtiV1Model) ToJWT(c *plugnmeet.LtiClaims) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(config.GetConfig().Client.Secret)},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    config.GetConfig().Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		Expiry:    jwt.NewNumericDate(time.Now().UTC().Add(time.Hour * 2)), // valid for 2 hours
		Subject:   c.UserId,
	}

	return jwt.Signed(sig).Claims(cl).Claims(c).Serialize()
}

func (m *LtiV1Model) LTIV1VerifyHeaderToken(token string) (*LtiClaims, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return nil, err
	}

	out := jwt.Claims{}
	claims := &LtiClaims{}
	if err = tok.Claims([]byte(config.GetConfig().Client.Secret), &out, claims); err != nil {
		return nil, err
	}
	if err = out.Validate(jwt.Expected{Issuer: config.GetConfig().Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		return nil, err
	}

	return claims, nil
}
