package models

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// GetAnalyticsDownloadToken will use the same JWT token generator as plugNmeet is using
func (m *AnalyticsModel) GetAnalyticsDownloadToken(r *plugnmeet.GetAnalyticsDownloadTokenReq) (string, error) {
	analytic, err := m.fetchAnalytic(r.FileId)
	if err != nil {
		return "", err
	}

	return m.generateToken(analytic.FileName)
}

func (m *AnalyticsModel) generateToken(fileName string) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(m.app.Client.Secret)}, (&jose.SignerOptions{}).WithType("JWT"))

	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    m.app.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		Expiry:    jwt.NewNumericDate(time.Now().UTC().Add(*m.app.AnalyticsSettings.TokenValidity)),
		Subject:   fileName,
	}

	return jwt.Signed(sig).Claims(cl).Serialize()
}

// VerifyAnalyticsToken verify token & provide file path
func (m *AnalyticsModel) VerifyAnalyticsToken(token string) (string, int, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(m.app.Client.Secret), &out); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{
		Issuer: m.app.Client.ApiKey,
		Time:   time.Now().UTC(),
	}); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	file := fmt.Sprintf("%s/%s", *m.app.AnalyticsSettings.FilesStorePath, out.Subject)
	_, err = os.Lstat(file)
	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return "", fiber.StatusNotFound, errors.New(ms[len(ms)-1])
	}

	return file, fiber.StatusOK, nil
}
