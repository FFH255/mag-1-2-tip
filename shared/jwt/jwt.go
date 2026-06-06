package jwt

import (
	"time"

	"github.com/goccy/go-json"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pkg/errors"
)

var (
	InvalidAccessTokenError = errors.New("invalid access token")
	ExpiredAccessTokenError = errors.New("expired access token")

	signingMethod = jwt.SigningMethodHS256
	issuer        = "vvadfadeev"
)

func Create[P any](secret string, payload P, exp time.Duration) (string, error) {
	subject, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		Subject:   string(subject[:]),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(exp)),
	}

	token := jwt.NewWithClaims(signingMethod, claims)

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

func Validate[P any](jwtToken, secret string) (P, error) {
	var p P

	keyFunc := func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.Wrap(InvalidAccessTokenError, "unexpected signing method")
		}

		return []byte(secret), nil
	}

	token, err := jwt.ParseWithClaims(jwtToken, &jwt.RegisteredClaims{}, keyFunc)
	if err != nil {
		return p, errors.Wrap(InvalidAccessTokenError, err.Error())
	}

	claims, err := isValidToken(token)
	if err != nil {
		return p, err
	}

	err = json.Unmarshal([]byte(claims.Subject), &p)
	if err != nil {
		return p, errors.Wrap(InvalidAccessTokenError, err.Error())
	}

	return p, nil
}

func isValidToken(token *jwt.Token) (*jwt.RegisteredClaims, error) {
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, InvalidAccessTokenError
	}

	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, ExpiredAccessTokenError
	}

	return claims, nil
}
