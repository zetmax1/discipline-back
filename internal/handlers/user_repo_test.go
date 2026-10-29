package handlers

import (
	"gorm.io/gorm"

	"discipline-backend/internal/models"
)

// handlerUserRepo is an in-memory UserRepository for handler tests.
type handlerUserRepo struct {
	users  map[uint]*models.User
	nextID uint
}

func newHandlerUserRepo() *handlerUserRepo {
	return &handlerUserRepo{users: map[uint]*models.User{}, nextID: 1}
}

func (r *handlerUserRepo) Create(u *models.User) error {
	u.ID = r.nextID
	r.nextID++
	cp := *u
	r.users[u.ID] = &cp
	return nil
}
func (r *handlerUserRepo) FindByEmail(email string) (*models.User, error) {
	for _, u := range r.users {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *handlerUserRepo) FindByID(id uint) (*models.User, error) {
	if u, ok := r.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *handlerUserRepo) Update(u *models.User) error {
	cp := *u
	r.users[u.ID] = &cp
	return nil
}
