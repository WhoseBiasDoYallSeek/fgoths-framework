// Package models contains data models and business entities.
// Models represent the core domain objects of the application.
package models

import "time"

// User represents a user in the system
type User struct {
	ID        uint64    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// NewUser creates a new User instance
func NewUser(name, email string) *User {
	return &User{
		Name:      name,
		Email:     email,
		CreatedAt: time.Now(),
	}
}
