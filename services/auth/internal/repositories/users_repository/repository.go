package users_repository

import (
	"context"

	"github.com/FFH255/mag-1-2-tip/services/auth/internal/models"
)

type Repository struct {
	users map[string]models.User
}

func New() *Repository {
	return &Repository{
		users: make(map[string]models.User),
	}
}

func (r *Repository) Exists(ctx context.Context, login string) (bool, error) {
	_, ok := r.users[login]

	return ok, nil
}

func (r *Repository) GetByLogin(ctx context.Context, login string) (*models.User, error) {
	user, exists := r.users[login]
	if !exists {
		return nil, nil
	}

	return &user, nil
}

func (r *Repository) Save(ctx context.Context, user *models.User) error {
	r.users[user.Login] = *user

	return nil
}
