package auth

import (
	"context"
	"fmt"

	"github.com/FFH255/mag-1-2-tip/services/auth/models"
)

var (
	UserNotFoundError      = fmt.Errorf("user not found")
	IncorrectPasswordError = fmt.Errorf("incorrect password")
)

type Service struct {
	userRepository userRepository
}

type userRepository interface {
	GetByLogin(ctx context.Context, login string) (*models.User, error)
	Save(ctx context.Context, user *models.User) error
}

func New(userRepository userRepository) Service {
	return Service{
		userRepository: userRepository,
	}
}

func (s Service) Login(ctx context.Context, login, password string) (models.Token, error) {
	user, err := s.userRepository.GetByLogin(ctx, login)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", UserNotFoundError
	}

	if user.Password != password {
		return "", IncorrectPasswordError
	}

	return "", nil
}

func (s Service) Verify(ctx context.Context, token models.Token) (bool, error) {
	return false, nil
}
