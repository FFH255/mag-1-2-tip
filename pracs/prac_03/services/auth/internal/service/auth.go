package service

import (
	"errors"
	"strings"
)

type AuthService struct{}

var ErrInvalidToken = errors.New("invalid token")

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

type VerifyResponse struct {
	Valid   bool   `json:"valid"`
	Subject string `json:"subject,omitempty"`
	Error   string `json:"error,omitempty"`
}

func NewAuthService() *AuthService {
	return &AuthService{}
}

func (s *AuthService) Login(req LoginRequest) (LoginResponse, bool) {
	if req.Username == "student" && req.Password == "student" {
		return LoginResponse{
			AccessToken: "demo-token",
			TokenType:   "Bearer",
		}, true
	}

	return LoginResponse{}, false
}

func (s *AuthService) Verify(authHeader string) VerifyResponse {
	token, err := bearerToken(authHeader)
	if err != nil {
		return VerifyResponse{Valid: false, Error: "unauthorized"}
	}

	subject, err := s.VerifyToken(token)
	if err != nil {
		return VerifyResponse{Valid: false, Error: "unauthorized"}
	}

	return VerifyResponse{Valid: true, Subject: subject}
}

func (s *AuthService) VerifyToken(token string) (string, error) {
	if strings.TrimSpace(token) != "demo-token" {
		return "", ErrInvalidToken
	}

	return "student", nil
}

func bearerToken(authHeader string) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", ErrInvalidToken
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if token == "" {
		return "", ErrInvalidToken
	}

	return token, nil
}
