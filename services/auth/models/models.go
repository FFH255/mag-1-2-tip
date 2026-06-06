package models

type User struct {
	Login    string
	Password string
}

func NewUser(login, password string) *User {
	return &User{
		Login:    login,
		Password: password,
	}
}

type Token string

type Payload struct {
	Login string `json:"login"`
}

func NewPayload(login string) *Payload {
	return &Payload{
		Login: login,
	}
}
