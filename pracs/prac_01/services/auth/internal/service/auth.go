package service

import "strings"

type AuthService struct{}

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
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return VerifyResponse{Valid: false, Error: "unauthorized"}
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	if token != "demo-token" {
		return VerifyResponse{Valid: false, Error: "unauthorized"}
	}

	return VerifyResponse{Valid: true, Subject: "student"}
}
