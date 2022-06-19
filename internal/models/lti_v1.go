package models

import (
	"errors"
	"github.com/jordic/lti"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"net/url"
	"strings"
)

type LTIV1 struct {
}

func NewLTIV1Model() *LTIV1 {
	return &LTIV1{}
}

func (m *LTIV1) VerifyAuth(requests, signingURL string) (*url.Values, error) {
	r := strings.Split(requests, "&")
	p := lti.NewProvider(config.AppCnf.Client.Secret, signingURL)
	p.Method = "POST"
	p.ConsumerKey = config.AppCnf.Client.ApiKey
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
		return nil, errors.New("invalid consumer_key")
	}

	sign, err := p.Sign()
	if err != nil {
		return nil, err
	}
	params := p.Params()

	if sign != providedSignature {
		return nil, errors.New("verification failed")
	}

	return &params, nil
}
