package auth_service

import (
	"context"
	"fmt"
	"time"

	"github.com/FFH255/mag-1-2-tip/services/auth/internal/models"
	"github.com/FFH255/mag-1-2-tip/shared/jwt"
)

var (
	UserNotFoundError      = fmt.Errorf("user not found")
	IncorrectPasswordError = fmt.Errorf("incorrect password")
	UserAlreadyExistsError = fmt.Errorf("user already exists")
)

type Service struct {
	secret         string
	expireDuration time.Duration
	userRepository userRepository
}

type userRepository interface {
	Exists(ctx context.Context, login string) (bool, error)
	GetByLogin(ctx context.Context, login string) (*models.User, error)
	Save(ctx context.Context, user *models.User) error
}

func New(secret string, expireDuration time.Duration, userRepository userRepository) Service {
	return Service{
		userRepository: userRepository,
		secret:         secret,
		expireDuration: expireDuration,
	}
}

func (s Service) Register(ctx context.Context, login, password string) error {
	exists, err := s.userRepository.Exists(ctx, login)
	if err != nil {
		return err
	}
	if exists {
		return UserAlreadyExistsError
	}

	user := models.NewUser(login, password)
	if err := s.userRepository.Save(ctx, user); err != nil {
		return err
	}

	return nil
}

func (s Service) Login(ctx context.Context, login, password string) (models.AccessToken, models.AccessTokenType, error) {
	user, err := s.userRepository.GetByLogin(ctx, login)
	if err != nil {
		return "", "", err
	}
	if user == nil {
		return "", "", UserNotFoundError
	}

	if user.Password != password {
		return "", "", IncorrectPasswordError
	}

	token, err := jwt.Create[models.Payload](s.secret, *models.NewPayload(login), s.expireDuration)
	if err != nil {
		return "", "", err
	}

	return models.AccessToken(token), models.BearerAccessTokenType, nil
}

func (s Service) Verify(ctx context.Context, token models.AccessToken) error {
	_, err := jwt.Validate[models.Payload](string(token), s.secret)
	if err != nil {
		return err
	}

	return nil
}
