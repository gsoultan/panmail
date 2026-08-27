package smtp

import (
	"errors"

	"github.com/emersion/go-sasl"
)

// loginChallenges are what a LOGIN exchange sends, in order.
var loginChallenges = [][]byte{
	[]byte("Username:"),
	[]byte("Password:"),
}

// loginServer implements the LOGIN mechanism.
//
// go-sasl ships a LOGIN client but no server, and LOGIN is obsolete in favour
// of PLAIN. It is implemented here anyway because a good deal of existing
// application tooling sends nothing else, and this transport exists to accept
// mail from applications that are already written.
type loginServer struct {
	authenticate func(username, password string) error
	username     string
	step         int
}

// newLoginServer returns a SASL server for the LOGIN mechanism.
func newLoginServer(authenticate func(username, password string) error) sasl.Server {
	return &loginServer{authenticate: authenticate}
}

// Next advances the exchange: challenge for a username, then for a password,
// then authenticate.
func (s *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch s.step {
	case 0:
		// A client may send the username as an initial response, in which
		// case the first challenge is skipped.
		if response == nil {
			s.step = 1
			return loginChallenges[0], false, nil
		}
		s.username = string(response)
		s.step = 2
		return loginChallenges[1], false, nil

	case 1:
		if response == nil {
			return nil, true, errors.New("sasl: expected a username")
		}
		s.username = string(response)
		s.step = 2
		return loginChallenges[1], false, nil

	case 2:
		if response == nil {
			return nil, true, errors.New("sasl: expected a password")
		}
		s.step = 3
		return nil, true, s.authenticate(s.username, string(response))

	default:
		return nil, true, sasl.ErrUnexpectedClientResponse
	}
}
