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

type AccessToken string

func (t AccessToken) String() string {
	return string(t)
}

type AccessTokenType string

func (t AccessTokenType) String() string {
	return string(t)
}

const (
	BearerAccessTokenType AccessTokenType = "Bearer"
)

type Payload struct {
	Login string `json:"login"`
}

func NewPayload(login string) *Payload {
	return &Payload{
		Login: login,
	}
}
