package auth

import (
	"crypto/rand"
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

var ErrEmptyPassword = errors.New("auth: password must not be empty")

type Hasher struct {
	cost  int
	dummy func() []byte
}

var DefaultHasher = NewHasher(bcrypt.DefaultCost)

func NewHasher(cost int) Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return Hasher{
		cost:  cost,
		dummy: sync.OnceValue(func() []byte { return newDummyHash(cost) }),
	}
}

func (h Hasher) Hash(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), h.effectiveCost())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h Hasher) Cost() int { return h.effectiveCost() }

func (h Hasher) effectiveCost() int {
	if h.cost == 0 {
		return bcrypt.DefaultCost
	}
	return h.cost
}

func newDummyHash(cost int) []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("auth: cannot generate dummy hash seed: " + err.Error())
	}
	h, err := bcrypt.GenerateFromPassword(secret, cost)
	if err != nil {
		// Only reachable if bcrypt itself is broken, in which case no
		// authentication is possible anyway. The cost is already validated by
		// NewHasher, so it cannot be the cause.
		panic("auth: cannot generate dummy hash: " + err.Error())
	}
	return h
}

func (h Hasher) VerifyNothing(password string) bool {
	dummy := h.dummy
	if dummy == nil {
		dummy = DefaultHasher.dummy
	}
	_ = bcrypt.CompareHashAndPassword(dummy(), []byte(password))
	return false
}

func VerifyPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
