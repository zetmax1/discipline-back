package services

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
)

// memUserRepo is an in-memory UserRepository for tests.
type memUserRepo struct {
	users  map[string]*models.User
	nextID uint
}

func newMemUserRepo() *memUserRepo { return &memUserRepo{users: map[string]*models.User{}, nextID: 1} }

func (r *memUserRepo) Create(u *models.User) error {
	u.ID = r.nextID
	r.nextID++
	cp := *u
	r.users[u.Email] = &cp
	return nil
}
func (r *memUserRepo) FindByEmail(email string) (*models.User, error) {
	if u, ok := r.users[email]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *memUserRepo) FindByID(id uint) (*models.User, error) {
	for _, u := range r.users {
		if u.ID == id {
			cp := *u
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *memUserRepo) Update(u *models.User) error {
	for email, existing := range r.users {
		if existing.ID == u.ID {
			delete(r.users, email)
		}
	}
	cp := *u
	r.users[u.Email] = &cp
	return nil
}

const testSecret = "jwt-secret-for-tests-0123456789abcdef"

func newTestAuthService() (AuthService, *memUserRepo) {
	repo := newMemUserRepo()
	return NewAuthService(repo, testSecret, 1), repo
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		name, password string
		ok             bool
	}{
		{"9 chars", "123456789", false},
		{"10 chars", "1234567890", true},
		{"72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		{"10 multibyte runes", strings.Repeat("é", 10), true},
		{"9 multibyte runes", strings.Repeat("é", 9), false},
		{"37 multibyte runes = 74 bytes", strings.Repeat("é", 37), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidatePassword ok=%v, got err=%v", tc.ok, err)
			}
			if err != nil && !errors.Is(err, ErrPasswordPolicy) {
				t.Fatalf("policy errors must wrap ErrPasswordPolicy, got %v", err)
			}
		})
	}
}

func TestRegister_EnforcesPasswordPolicy(t *testing.T) {
	svc, repo := newTestAuthService()
	if _, _, err := svc.Register("Ann", "ann@example.com", "short"); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("expected a password policy error, got %v", err)
	}
	if len(repo.users) != 0 {
		t.Fatal("no user may be created with a weak password")
	}
	if _, _, err := svc.Register("Ann", "ann@example.com", "long-enough-pw"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestRegisterAdmin_EnforcesPasswordPolicy(t *testing.T) {
	svc, _ := newTestAuthService()
	if _, _, err := svc.RegisterAdmin("Root", "root@example.com", strings.Repeat("x", 73)); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("expected a password policy error, got %v", err)
	}
}

func TestRegister_DuplicateEmailStillConflicts(t *testing.T) {
	svc, _ := newTestAuthService()
	if _, _, err := svc.Register("Ann", "ann@example.com", "long-enough-pw"); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Register("Ann", "ann@example.com", "long-enough-pw")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestUpdateProfile_EnforcesPasswordPolicyOnChange(t *testing.T) {
	svc, _ := newTestAuthService()
	u, _, err := svc.Register("Ann", "ann@example.com", "long-enough-pw")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateProfile(u.ID, "", "", "long-enough-pw", "123456789", "")
	if !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("expected a password policy error on change, got %v", err)
	}
	if _, _, err := svc.Login("ann@example.com", "long-enough-pw"); err != nil {
		t.Fatal("old password must still work after a rejected change")
	}
}

func TestLogin_UnknownEmailRunsDummyCompare(t *testing.T) {
	svc, _ := newTestAuthService()
	calls := 0
	orig := compareHashAndPassword
	compareHashAndPassword = func(hash, pw []byte) error {
		calls++
		return orig(hash, pw)
	}
	t.Cleanup(func() { compareHashAndPassword = orig })

	_, _, err := svc.Login("ghost@example.com", "whatever-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("unknown email must still run one bcrypt compare, ran %d", calls)
	}
}

func TestDummyHashUsesDefaultCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash)
	if err != nil || cost != bcrypt.DefaultCost {
		t.Fatalf("dummy hash must use the same cost as real hashes, got %d (%v)", cost, err)
	}
}

func TestUpdateProfile_EmailChangeNeedsCorrectPassword(t *testing.T) {
	svc, _ := newTestAuthService()
	u, _, _ := svc.Register("Ann", "ann@example.com", "long-enough-pw")

	if _, err := svc.UpdateProfile(u.ID, "", "new@example.com", "", "", ""); err == nil {
		t.Fatal("email change without current password must fail")
	}
	if _, err := svc.UpdateProfile(u.ID, "", "new@example.com", "wrong-password", "", ""); err == nil {
		t.Fatal("email change with wrong password must fail")
	}
	updated, err := svc.UpdateProfile(u.ID, "", "new@example.com", "long-enough-pw", "", "")
	if err != nil || updated.Email != "new@example.com" {
		t.Fatalf("expected email change, got %v %+v", err, updated)
	}
}
